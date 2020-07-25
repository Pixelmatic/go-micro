// Package etcd provides an etcd service registry
package etcd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coreos/etcd/clientv3"
	"github.com/coreos/etcd/etcdserver/api/v3rpc/rpctypes"
	"github.com/coreos/etcd/mvcc/mvccpb"
	"github.com/micro/go-micro/v2/logger"
	"github.com/micro/go-micro/v2/registry"
	hash "github.com/mitchellh/hashstructure"
	"go.uber.org/zap"
)

const (
	prefix        = "/platform"
	defaultDomain = "inf"
)

type etcdRegistry struct {
	client  *clientv3.Client
	options registry.Options

	// register and leases are grouped by domain
	sync.RWMutex
	register map[string]register
	leases   map[string]leases
}

type register map[string]uint64
type leases map[string]clientv3.LeaseID

// NewRegistry returns an initialized etcd registry
func NewRegistry(opts ...registry.Option) registry.Registry {
	e := &etcdRegistry{
		options:  registry.Options{},
		register: make(map[string]register),
		leases:   make(map[string]leases),
	}
	configure(e, opts...)
	return e
}

func configure(e *etcdRegistry, opts ...registry.Option) error {
	config := clientv3.Config{
		Endpoints: []string{"127.0.0.1:2379"},
	}
	for _, o := range opts {
		o(&e.options)
	}

	if e.options.Timeout == 0 {
		e.options.Timeout = 5 * time.Second
	}

	if e.options.Secure || e.options.TLSConfig != nil {
		tlsConfig := e.options.TLSConfig
		if tlsConfig == nil {
			tlsConfig = &tls.Config{
				InsecureSkipVerify: true,
			}
		}

		config.TLS = tlsConfig
	}

	if e.options.Context != nil {
		u, ok := e.options.Context.Value(authKey{}).(*authCreds)
		if ok {
			config.Username = u.Username
			config.Password = u.Password
		}
		cfg, ok := e.options.Context.Value(logConfigKey{}).(*zap.Config)
		if ok && cfg != nil {
			config.LogConfig = cfg
		}
	}

	var cAddrs []string

	for _, address := range e.options.Addrs {
		if len(address) == 0 {
			continue
		}
		addr, port, err := net.SplitHostPort(address)
		if ae, ok := err.(*net.AddrError); ok && ae.Err == "missing port in address" {
			port = "2379"
			addr = address
			cAddrs = append(cAddrs, net.JoinHostPort(addr, port))
		} else if err == nil {
			cAddrs = append(cAddrs, net.JoinHostPort(addr, port))
		}
	}

	// if we got addrs then we'll update
	if len(cAddrs) > 0 {
		config.Endpoints = cAddrs
	}

	cli, err := clientv3.New(config)
	if err != nil {
		return err
	}
	e.client = cli
	return nil
}

func encode(s *registry.Service) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func decode(ds []byte) *registry.Service {
	var s *registry.Service
	json.Unmarshal(ds, &s)
	return s
}

func nodePath(domain, s, id string) string {
	service := strings.Replace(s, "/", "-", -1)
	node := strings.Replace(id, "/", "-", -1)
	return path.Join(prefixWithDomain(domain), service, node)
}

func servicePath(domain, s string) string {
	return path.Join(prefixWithDomain(domain), serializeServiceName(s))
}

func serializeServiceName(s string) string {
	return strings.ReplaceAll(s, "/", "-")
}

func prefixWithDomain(domain string) string {
	return path.Join(prefix, domain)
}

func (e *etcdRegistry) Init(opts ...registry.Option) error {
	return configure(e, opts...)
}

func (e *etcdRegistry) Options() registry.Options {
	return e.options
}

func (e *etcdRegistry) registerNode(s *registry.Service, node *registry.Node, opts ...registry.RegisterOption) error {
	if len(s.Nodes) == 0 {
		return errors.New("Require at least one node")
	}

	// parse the options
	var options registry.RegisterOptions
	for _, o := range opts {
		o(&options)
	}
	if len(options.Domain) == 0 {
		options.Domain = defaultDomain
	}

	// set the domain in metadata so it can be retrieved by wildcard queries
	if s.Metadata == nil {
		s.Metadata = map[string]string{"domain": options.Domain}
	} else {
		s.Metadata["domain"] = options.Domain
	}

	e.Lock()
	// ensure the leases and registers are setup for this domain
	if _, ok := e.leases[options.Domain]; !ok {
		e.leases[options.Domain] = make(leases)
	}
	if _, ok := e.register[options.Domain]; !ok {
		e.register[options.Domain] = make(register)
	}

	// check to see if we already have a lease cached
	leaseID, ok := e.leases[options.Domain][s.Name+node.Id]
	e.Unlock()

	if !ok {
		// missing lease, check if the key exists
		ctx, cancel := context.WithTimeout(context.Background(), e.options.Timeout)
		defer cancel()

		// look for the existing key
		key := nodePath(options.Domain, s.Name, node.Id)
		rsp, err := e.client.Get(ctx, key, clientv3.WithSerializable())
		if err != nil {
			return err
		}

		// get the existing lease
		for _, kv := range rsp.Kvs {
			if kv.Lease > 0 {
				leaseID = clientv3.LeaseID(kv.Lease)

				// decode the existing node
				srv := decode(kv.Value)
				if srv == nil || len(srv.Nodes) == 0 {
					continue
				}

				// create hash of service; uint64
				h, err := hash.Hash(srv.Nodes[0], nil)
				if err != nil {
					continue
				}

				// save the info
				e.Lock()
				e.leases[options.Domain][s.Name+node.Id] = leaseID
				e.register[options.Domain][s.Name+node.Id] = h
				e.Unlock()

				break
			}
		}
	}

	var leaseNotFound bool

	// renew the lease if it exists
	if leaseID > 0 {
		if logger.V(logger.TraceLevel, logger.DefaultLogger) {
			logger.Tracef("Renewing existing lease for %s %d", s.Name, leaseID)
		}

		if _, err := e.client.KeepAliveOnce(context.TODO(), leaseID); err != nil {
			if err != rpctypes.ErrLeaseNotFound {
				return err
			}

			if logger.V(logger.TraceLevel, logger.DefaultLogger) {
				logger.Tracef("Lease not found for %s %d", s.Name, leaseID)
			}

			// lease not found do register
			leaseNotFound = true
		}
	}

	// create hash of service; uint64
	h, err := hash.Hash(node, nil)
	if err != nil {
		return err
	}

	// get existing hash for the service node
	e.RLock()
	v, ok := e.register[options.Domain][s.Name+node.Id]
	e.RUnlock()

	// the service is unchanged, skip registering
	if ok && v == h && !leaseNotFound {
		if logger.V(logger.TraceLevel, logger.DefaultLogger) {
			logger.Tracef("Service %s node %s unchanged skipping registration", s.Name, node.Id)
		}
		return nil
	}

	// add domain to the service metadata so it can be determined when doing wildcard queries
	if s.Metadata == nil {
		s.Metadata = map[string]string{"domain": options.Domain}
	} else {
		s.Metadata["domain"] = options.Domain
	}

	service := &registry.Service{
		Name:      s.Name,
		Version:   s.Version,
		Metadata:  s.Metadata,
		Endpoints: s.Endpoints,
		Nodes:     []*registry.Node{node},
	}

	ctx, cancel := context.WithTimeout(context.Background(), e.options.Timeout)
	defer cancel()

	var lgr *clientv3.LeaseGrantResponse
	if options.TTL.Seconds() > 0 {
		// get a lease used to expire keys since we have a ttl
		lgr, err = e.client.Grant(ctx, int64(options.TTL.Seconds()))
		if err != nil {
			return err
		}
	}

	if logger.V(logger.TraceLevel, logger.DefaultLogger) {
		logger.Tracef("Registering %s id %s with ttl %v", service.Name, node.Id, options.TTL)
	}

	// create an entry for the node
	var putOpts []clientv3.OpOption
	if lgr != nil {
		putOpts = append(putOpts, clientv3.WithLease(lgr.ID))
	}

	key := nodePath(options.Domain, s.Name, node.Id)
	if _, err = e.client.Put(ctx, key, encode(service), putOpts...); err != nil {
		return err
	}

	e.Lock()
	// save our hash of the service
	e.register[options.Domain][s.Name+node.Id] = h
	// save our leaseID of the service
	if lgr != nil {
		e.leases[options.Domain][s.Name+node.Id] = lgr.ID
	}
	e.Unlock()

	return nil
}

func (e *etcdRegistry) Deregister(s *registry.Service, opts ...registry.DeregisterOption) error {
	if len(s.Nodes) == 0 {
		return errors.New("Require at least one node")
	}

	// parse the options
	var options registry.DeregisterOptions
	for _, o := range opts {
		o(&options)
	}
	if len(options.Domain) == 0 {
		options.Domain = defaultDomain
	}

	for _, node := range s.Nodes {
		e.Lock()
		// delete our hash of the service
		delete(e.register, s.Name+node.Id)
		// delete our lease of the service
		delete(e.leases, s.Name+node.Id)
		e.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), e.options.Timeout)
		defer cancel()

		if logger.V(logger.TraceLevel, logger.DefaultLogger) {
			logger.Tracef("Deregistering %s id %s", s.Name, node.Id)
		}

		if _, err := e.client.Delete(ctx, nodePath(options.Domain, s.Name, node.Id)); err != nil {
			return err
		}
	}

	return nil
}

func (e *etcdRegistry) Register(s *registry.Service, opts ...registry.RegisterOption) error {
	if len(s.Nodes) == 0 {
		return errors.New("Require at least one node")
	}

	var gerr error

	// register each node individually
	for _, node := range s.Nodes {
		if err := e.registerNode(s, node, opts...); err != nil {
			gerr = err
		}
	}

	return gerr
}

func (e *etcdRegistry) GetService(name string, opts ...registry.GetOption) ([]*registry.Service, error) {
	ctx, cancel := context.WithTimeout(context.Background(), e.options.Timeout)
	defer cancel()

	// parse the options and fallback to the default domain
	var options registry.GetOptions
	for _, o := range opts {
		o(&options)
	}
	if len(options.Domain) == 0 {
		options.Domain = defaultDomain
	}

	var results []*mvccpb.KeyValue
	if options.Domain == registry.WildcardDomain {
		rsp, err := e.client.Get(ctx, prefix, clientv3.WithPrefix(), clientv3.WithSerializable())
		if err != nil {
			return nil, err
		}

		// filter using a check for the service name
		keyPath := fmt.Sprintf("/%v/", serializeServiceName(name))
		for _, kv := range rsp.Kvs {
			if strings.Contains(string(kv.Key), keyPath) {
				results = append(results, kv)
			}
		}
	} else {
		prefix := servicePath(options.Domain, name) + "/"
		rsp, err := e.client.Get(ctx, prefix, clientv3.WithPrefix(), clientv3.WithSerializable())
		if err != nil {
			return nil, err
		}
		results = rsp.Kvs
	}

	if len(results) == 0 {
		return nil, registry.ErrNotFound
	}

	versions := make(map[string]*registry.Service)

	for _, n := range results {
		// key contains the domain, service name and version. hence, if a service name exists in two
		// seperate domains, it'll be returned twice (for wildcard queries), this is because although
		// the name is the same, the endpoints / metadata could differ
		key, _ := path.Split(string(n.Key))

		if sn := decode(n.Value); sn != nil {
			s, ok := versions[key]
			if !ok {
				s = &registry.Service{
					Name:      sn.Name,
					Version:   sn.Version,
					Metadata:  sn.Metadata,
					Endpoints: sn.Endpoints,
				}
				versions[s.Version] = s
			}

			s.Nodes = append(s.Nodes, sn.Nodes...)
		}
	}

	services := make([]*registry.Service, 0, len(versions))
	for _, service := range versions {
		services = append(services, service)
	}

	return services, nil
}

func (e *etcdRegistry) ListServices(opts ...registry.ListOption) ([]*registry.Service, error) {
	// parse the options
	var options registry.ListOptions
	for _, o := range opts {
		o(&options)
	}
	if len(options.Domain) == 0 {
		options.Domain = defaultDomain
	}

	// determine the prefix
	var p string
	if options.Domain == registry.WildcardDomain {
		p = prefix
	} else {
		p = prefixWithDomain(options.Domain)
	}

	ctx, cancel := context.WithTimeout(context.Background(), e.options.Timeout)
	defer cancel()

	rsp, err := e.client.Get(ctx, p, clientv3.WithPrefix(), clientv3.WithSerializable())
	if err != nil {
		return nil, err
	}
	if len(rsp.Kvs) == 0 {
		return []*registry.Service{}, nil
	}

	versions := make(map[string]*registry.Service)
	for _, n := range rsp.Kvs {
		sn := decode(n.Value)

		if sn == nil {
			continue
		}

		// key contains the domain, service name and version. hence, if a service name exists in two
		// seperate domains, it'll be returned twice (for wildcard queries), this is because although
		// the name is the same, the endpoints / metadata could differ
		key, _ := path.Split(string(n.Key))

		v, ok := versions[key]
		if !ok {
			versions[key] = sn
			continue
		}

		// append to service:version nodes
		v.Nodes = append(v.Nodes, sn.Nodes...)
	}

	services := make([]*registry.Service, 0, len(versions))
	for _, service := range versions {
		services = append(services, service)
	}

	// sort the services
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })

	return services, nil
}

func (e *etcdRegistry) Watch(opts ...registry.WatchOption) (registry.Watcher, error) {
	return newEtcdWatcher(e, e.options.Timeout, opts...)
}

func (e *etcdRegistry) String() string {
	return "etcd"
}
