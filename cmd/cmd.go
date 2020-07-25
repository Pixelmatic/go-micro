// Package cmd is an interface for building a command line binary
package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/micro/cli/v2"
	"github.com/micro/go-micro/v2/auth"
	"github.com/micro/go-micro/v2/broker"
	brokerSrv "github.com/micro/go-micro/v2/broker/service"
	"github.com/micro/go-micro/v2/client"
	"github.com/micro/go-micro/v2/client/grpc"
	"github.com/micro/go-micro/v2/config"
	"github.com/micro/go-micro/v2/debug/profile"
	"github.com/micro/go-micro/v2/debug/trace"
	"github.com/micro/go-micro/v2/logger"
	"github.com/micro/go-micro/v2/registry"
	registrySrv "github.com/micro/go-micro/v2/registry/service"
	"github.com/micro/go-micro/v2/router"
	srvRouter "github.com/micro/go-micro/v2/router/service"
	"github.com/micro/go-micro/v2/runtime"
	"github.com/micro/go-micro/v2/selector"
	"github.com/micro/go-micro/v2/server"
	"github.com/micro/go-micro/v2/store"
	"github.com/micro/go-micro/v2/transport"
	authutil "github.com/micro/go-micro/v2/util/auth"
	"github.com/micro/go-micro/v2/util/wrapper"

	configSrc "github.com/micro/go-micro/v2/config/source"
	configSrv "github.com/micro/go-micro/v2/config/source/service"
)

type Cmd interface {
	// Init initialises options
	// Note: Use Run to parse command line
	Init(opts ...Option) error
	// Options set within this command
	Options() Options
	// The cli app within this cmd
	App() *cli.App
	// Run executes the command
	Run() error
	// Implementation
	String() string
}

type cmd struct {
	opts Options
	app  *cli.App
}

type Option func(o *Options)

var (
	DefaultCmd = newCmd()

	DefaultFlags = []cli.Flag{
		&cli.StringFlag{
			Name:    "client",
			EnvVars: []string{"MICRO_CLIENT"},
			Usage:   "Client for go-micro; rpc",
		},
		&cli.StringFlag{
			Name:    "client_request_timeout",
			EnvVars: []string{"MICRO_CLIENT_REQUEST_TIMEOUT"},
			Usage:   "Sets the client request timeout. e.g 500ms, 5s, 1m. Default: 5s",
		},
		&cli.IntFlag{
			Name:    "client_retries",
			EnvVars: []string{"MICRO_CLIENT_RETRIES"},
			Value:   client.DefaultRetries,
			Usage:   "Sets the client retries. Default: 1",
		},
		&cli.IntFlag{
			Name:    "client_pool_size",
			EnvVars: []string{"MICRO_CLIENT_POOL_SIZE"},
			Usage:   "Sets the client connection pool size. Default: 1",
		},
		&cli.StringFlag{
			Name:    "client_pool_ttl",
			EnvVars: []string{"MICRO_CLIENT_POOL_TTL"},
			Usage:   "Sets the client connection pool ttl. e.g 500ms, 5s, 1m. Default: 1m",
		},
		&cli.IntFlag{
			Name:    "register_ttl",
			EnvVars: []string{"MICRO_REGISTER_TTL"},
			Value:   60,
			Usage:   "Register TTL in seconds",
		},
		&cli.IntFlag{
			Name:    "register_interval",
			EnvVars: []string{"MICRO_REGISTER_INTERVAL"},
			Value:   30,
			Usage:   "Register interval in seconds",
		},
		&cli.StringFlag{
			Name:    "server",
			EnvVars: []string{"MICRO_SERVER"},
			Usage:   "Server for go-micro; rpc",
		},
		&cli.StringFlag{
			Name:    "server_name",
			EnvVars: []string{"MICRO_SERVER_NAME"},
			Usage:   "Name of the server. go.micro.srv.example",
		},
		&cli.StringFlag{
			Name:    "server_version",
			EnvVars: []string{"MICRO_SERVER_VERSION"},
			Usage:   "Version of the server. 1.1.0",
		},
		&cli.StringFlag{
			Name:    "server_id",
			EnvVars: []string{"MICRO_SERVER_ID"},
			Usage:   "Id of the server. Auto-generated if not specified",
		},
		&cli.StringFlag{
			Name:    "server_address",
			EnvVars: []string{"MICRO_SERVER_ADDRESS"},
			Usage:   "Bind address for the server. 127.0.0.1:8080",
		},
		&cli.StringFlag{
			Name:    "server_advertise",
			EnvVars: []string{"MICRO_SERVER_ADVERTISE"},
			Usage:   "Used instead of the server_address when registering with discovery. 127.0.0.1:8080",
		},
		&cli.StringSliceFlag{
			Name:    "server_metadata",
			EnvVars: []string{"MICRO_SERVER_METADATA"},
			Value:   &cli.StringSlice{},
			Usage:   "A list of key-value pairs defining metadata. version=1.0.0",
		},
		&cli.StringFlag{
			Name:    "broker",
			EnvVars: []string{"MICRO_BROKER"},
			Usage:   "Broker for pub/sub. http, nats, rabbitmq",
		},
		&cli.StringFlag{
			Name:    "broker_address",
			EnvVars: []string{"MICRO_BROKER_ADDRESS"},
			Usage:   "Comma-separated list of broker addresses",
		},
		&cli.StringFlag{
			Name:    "broker_tls_ca",
			Usage:   "Certificate authority for TLS with broker",
			EnvVars: []string{"MICRO_BROKER_TLS_CA"},
		},
		&cli.StringFlag{
			Name:    "broker_tls_cert",
			Usage:   "Client cert for TLS with broker",
			EnvVars: []string{"MICRO_BROKER_TLS_CERT"},
		},
		&cli.StringFlag{
			Name:    "broker_tls_key",
			Usage:   "Client key for TLS with broker",
			EnvVars: []string{"MICRO_BROKER_TLS_KEY"},
		},
		&cli.StringFlag{
			Name:    "profile",
			Usage:   "Debug profiler for cpu and memory stats",
			EnvVars: []string{"MICRO_DEBUG_PROFILE"},
		},
		&cli.StringFlag{
			Name:    "registry",
			EnvVars: []string{"MICRO_REGISTRY"},
			Usage:   "Registry for discovery. etcd, mdns",
		},
		&cli.StringFlag{
			Name:    "registry_address",
			EnvVars: []string{"MICRO_REGISTRY_ADDRESS"},
			Usage:   "Comma-separated list of registry addresses",
		},
		&cli.StringFlag{
			Name:    "registry_tls_ca",
			Usage:   "Certificate authority for TLS with registry",
			EnvVars: []string{"MICRO_REGISTRY_TLS_CA"},
		},
		&cli.StringFlag{
			Name:    "registry_tls_cert",
			Usage:   "Client cert for TLS with registry",
			EnvVars: []string{"MICRO_REGISTRY_TLS_CERT"},
		},
		&cli.StringFlag{
			Name:    "registry_tls_key",
			Usage:   "Client key for TLS with registry",
			EnvVars: []string{"MICRO_REGISTRY_TLS_KEY"},
		}, &cli.StringFlag{
			Name:    "runtime",
			Usage:   "Runtime for building and running services e.g local, kubernetes",
			EnvVars: []string{"MICRO_RUNTIME"},
			Value:   "local",
		},
		&cli.StringFlag{
			Name:    "runtime_source",
			Usage:   "Runtime source for building and running services e.g github.com/micro/service",
			EnvVars: []string{"MICRO_RUNTIME_SOURCE"},
			Value:   "github.com/micro/services",
		},
		&cli.StringFlag{
			Name:    "selector",
			EnvVars: []string{"MICRO_SELECTOR"},
			Usage:   "Selector used to pick nodes for querying",
		},
		&cli.StringFlag{
			Name:    "store",
			EnvVars: []string{"MICRO_STORE"},
			Usage:   "Store used for key-value storage",
		},
		&cli.StringFlag{
			Name:    "store_address",
			EnvVars: []string{"MICRO_STORE_ADDRESS"},
			Usage:   "Comma-separated list of store addresses",
		},
		&cli.StringFlag{
			Name:    "store_database",
			EnvVars: []string{"MICRO_STORE_DATABASE"},
			Usage:   "Database option for the underlying store",
		},
		&cli.StringFlag{
			Name:    "store_table",
			EnvVars: []string{"MICRO_STORE_TABLE"},
			Usage:   "Table option for the underlying store",
		},
		&cli.StringFlag{
			Name:    "transport",
			EnvVars: []string{"MICRO_TRANSPORT"},
			Usage:   "Transport mechanism used; http",
		},
		&cli.StringFlag{
			Name:    "transport_address",
			EnvVars: []string{"MICRO_TRANSPORT_ADDRESS"},
			Usage:   "Comma-separated list of transport addresses",
		},
		&cli.StringFlag{
			Name:    "tracer",
			EnvVars: []string{"MICRO_TRACER"},
			Usage:   "Tracer for distributed tracing, e.g. memory, jaeger",
		},
		&cli.StringFlag{
			Name:    "tracer_address",
			EnvVars: []string{"MICRO_TRACER_ADDRESS"},
			Usage:   "Comma-separated list of tracer addresses",
		},
		&cli.StringFlag{
			Name:    "auth",
			EnvVars: []string{"MICRO_AUTH"},
			Usage:   "Auth for role based access control, e.g. service",
		},
		&cli.StringFlag{
			Name:    "auth_address",
			EnvVars: []string{"MICRO_AUTH_ADDRESS"},
			Usage:   "Comma-separated list of auth addresses",
		},
		&cli.StringFlag{
			Name:    "auth_id",
			EnvVars: []string{"MICRO_AUTH_ID"},
			Usage:   "Account ID used for client authentication",
		},
		&cli.StringFlag{
			Name:    "auth_secret",
			EnvVars: []string{"MICRO_AUTH_SECRET"},
			Usage:   "Account secret used for client authentication",
		},
		&cli.StringFlag{
			Name:    "service_namespace",
			EnvVars: []string{"MICRO_NAMESPACE"},
			Usage:   "Namespace the service is operating in",
			Value:   "inf",
		},
		&cli.StringFlag{
			Name:    "auth_public_key",
			EnvVars: []string{"MICRO_AUTH_PUBLIC_KEY"},
			Usage:   "Public key for JWT auth (base64 encoded PEM)",
		},
		&cli.StringFlag{
			Name:    "auth_private_key",
			EnvVars: []string{"MICRO_AUTH_PRIVATE_KEY"},
			Usage:   "Private key for JWT auth (base64 encoded PEM)",
		},
		&cli.StringFlag{
			Name:    "config",
			EnvVars: []string{"MICRO_CONFIG"},
			Usage:   "The source of the config to be used to get configuration",
		},
		&cli.StringFlag{
			Name:    "router",
			EnvVars: []string{"MICRO_ROUTER"},
			Usage:   "Router used for client requests",
		},
		&cli.StringFlag{
			Name:    "router_address",
			Usage:   "Comma-separated list of router addresses",
			EnvVars: []string{"MICRO_ROUTER_ADDRESS"},
		},
	}

	DefaultBrokers = map[string]func(...broker.Option) broker.Broker{}

	DefaultClients = map[string]func(...client.Option) client.Client{}

	DefaultRegistries = map[string]func(...registry.Option) registry.Registry{}

	DefaultRouters = map[string]func(...router.Option) router.Router{}

	DefaultSelectors = map[string]func(...selector.Option) selector.Selector{}

	DefaultServers = map[string]func(...server.Option) server.Server{}

	DefaultTransports = map[string]func(...transport.Option) transport.Transport{}

	DefaultRuntimes = map[string]func(...runtime.Option) runtime.Runtime{}

	DefaultStores = map[string]func(...store.Option) store.Store{}

	DefaultTracers = map[string]func(...trace.Option) trace.Tracer{}

	DefaultAuths = map[string]func(...auth.Option) auth.Auth{}

	DefaultProfiles = map[string]func(...profile.Option) profile.Profile{}

	DefaultConfigs = map[string]func(...config.Option) (config.Config, error){}
)

func init() {
	rand.Seed(time.Now().Unix())
}

func newCmd(opts ...Option) Cmd {
	options := Options{
		Auth:      &auth.DefaultAuth,
		Broker:    &broker.DefaultBroker,
		Client:    &client.DefaultClient,
		Registry:  &registry.DefaultRegistry,
		Server:    &server.DefaultServer,
		Selector:  &selector.DefaultSelector,
		Transport: &transport.DefaultTransport,
		Router:    &router.DefaultRouter,
		Runtime:   &runtime.DefaultRuntime,
		Store:     &store.DefaultStore,
		Tracer:    &trace.DefaultTracer,
		Profile:   &profile.DefaultProfile,
		Config:    &config.DefaultConfig,

		Brokers:    DefaultBrokers,
		Clients:    DefaultClients,
		Registries: DefaultRegistries,
		Selectors:  DefaultSelectors,
		Servers:    DefaultServers,
		Transports: DefaultTransports,
		Routers:    DefaultRouters,
		Runtimes:   DefaultRuntimes,
		Stores:     DefaultStores,
		Tracers:    DefaultTracers,
		Auths:      DefaultAuths,
		Profiles:   DefaultProfiles,
		Configs:    DefaultConfigs,
	}

	for _, o := range opts {
		o(&options)
	}

	if len(options.Description) == 0 {
		options.Description = "a go-micro service"
	}

	cmd := new(cmd)
	cmd.opts = options
	cmd.app = cli.NewApp()
	cmd.app.Name = cmd.opts.Name
	cmd.app.Version = cmd.opts.Version
	cmd.app.Usage = cmd.opts.Description
	cmd.app.Before = cmd.Before
	cmd.app.Flags = DefaultFlags
	cmd.app.Action = func(c *cli.Context) error {
		return nil
	}

	if len(options.Version) == 0 {
		cmd.app.HideVersion = true
	}

	return cmd
}

func (c *cmd) App() *cli.App {
	return c.app
}

func (c *cmd) Options() Options {
	return c.opts
}

func (c *cmd) Before(ctx *cli.Context) error {
	// Setup client options
	var clientOpts []client.Option

	if r := ctx.Int("client_retries"); r >= 0 {
		clientOpts = append(clientOpts, client.Retries(r))
	}

	if t := ctx.String("client_request_timeout"); len(t) > 0 {
		d, err := time.ParseDuration(t)
		if err != nil {
			logger.Fatalf("failed to parse client_request_timeout: %v", t)
		}
		clientOpts = append(clientOpts, client.RequestTimeout(d))
	}

	if r := ctx.Int("client_pool_size"); r > 0 {
		clientOpts = append(clientOpts, client.PoolSize(r))
	}

	if t := ctx.String("client_pool_ttl"); len(t) > 0 {
		d, err := time.ParseDuration(t)
		if err != nil {
			logger.Fatalf("failed to parse client_pool_ttl: %v", t)
		}
		clientOpts = append(clientOpts, client.PoolTTL(d))
	}

	// Setup server options
	var serverOpts []server.Option

	metadata := make(map[string]string)
	for _, d := range ctx.StringSlice("server_metadata") {
		var key, val string
		parts := strings.Split(d, "=")
		key = parts[0]
		if len(parts) > 1 {
			val = strings.Join(parts[1:], "=")
		}
		metadata[key] = val
	}

	if len(metadata) > 0 {
		serverOpts = append(serverOpts, server.Metadata(metadata))
	}

	if len(ctx.String("server_name")) > 0 {
		serverOpts = append(serverOpts, server.Name(ctx.String("server_name")))
	}

	if len(ctx.String("server_version")) > 0 {
		serverOpts = append(serverOpts, server.Version(ctx.String("server_version")))
	}

	if len(ctx.String("server_id")) > 0 {
		serverOpts = append(serverOpts, server.Id(ctx.String("server_id")))
	}

	if len(ctx.String("server_address")) > 0 {
		serverOpts = append(serverOpts, server.Address(ctx.String("server_address")))
	}

	if len(ctx.String("server_advertise")) > 0 {
		serverOpts = append(serverOpts, server.Advertise(ctx.String("server_advertise")))
	}

	if ttl := time.Duration(ctx.Int("register_ttl")); ttl >= 0 {
		serverOpts = append(serverOpts, server.RegisterTTL(ttl*time.Second))
	}

	if val := time.Duration(ctx.Int("register_interval")); val >= 0 {
		serverOpts = append(serverOpts, server.RegisterInterval(val*time.Second))
	}

	// setup a client to use when calling the runtime. It is important the auth client is wrapped
	// after the cache client since the wrappers are applied in reverse order and the cache will use
	// some of the headers set by the auth client.
	authFn := func() auth.Auth { return *c.opts.Auth }
	cacheFn := func() *client.Cache { return (*c.opts.Client).Options().Cache }
	microClient := wrapper.CacheClient(cacheFn, grpc.NewClient())
	microClient = wrapper.AuthClient(authFn, microClient)

	// Setup auth options
	authOpts := []auth.Option{auth.WithClient(microClient)}
	if len(ctx.String("auth_address")) > 0 {
		authOpts = append(authOpts, auth.Addrs(ctx.String("auth_address")))
	}
	if len(ctx.String("auth_id")) > 0 || len(ctx.String("auth_secret")) > 0 {
		authOpts = append(authOpts, auth.Credentials(
			ctx.String("auth_id"), ctx.String("auth_secret"),
		))
	}
	if len(ctx.String("auth_public_key")) > 0 {
		authOpts = append(authOpts, auth.PublicKey(ctx.String("auth_public_key")))
	}
	if len(ctx.String("auth_private_key")) > 0 {
		authOpts = append(authOpts, auth.PrivateKey(ctx.String("auth_private_key")))
	}
	if ns := ctx.String("service_namespace"); len(ns) > 0 {
		serverOpts = append(serverOpts, server.Namespace(ns))
		authOpts = append(authOpts, auth.Issuer(ns))
	}

	// Set the auth
	if name := ctx.String("auth"); len(name) > 0 {
		a, ok := c.opts.Auths[name]
		if !ok {
			logger.Fatalf("Unsupported auth: %s", name)
		}
		*c.opts.Auth = a(authOpts...)
		serverOpts = append(serverOpts, server.Auth(*c.opts.Auth))
	} else if len(authOpts) > 0 {
		(*c.opts.Auth).Init(authOpts...)
	}

	// verify the auth's service account
	if err := authutil.Verify(*c.opts.Auth); err != nil {
		if logger.V(logger.DebugLevel, logger.DefaultLogger) {
			logger.Debugf("Auth [%v] Error generating auth account: %v", (*c.opts.Auth), err)
		}
	}

	// Setup broker options.
	brokerOpts := []broker.Option{brokerSrv.Client(microClient)}

	if len(ctx.String("broker_address")) > 0 {
		brokerOpts = append(brokerOpts, broker.Addrs(ctx.String("broker_address")))
	}

	// Parse broker TLS certs
	if ctx.IsSet("broker_tls_cert") || ctx.IsSet("broker_tls_key") {
		cert, err := tls.LoadX509KeyPair(ctx.String("broker_tls_cert"), ctx.String("broker_tls_key"))
		if err != nil {
			logger.Fatalf("Error loading broker TLS cert: %v", err)
		}

		// load custom certificate authority
		caCertPool := x509.NewCertPool()
		if ctx.IsSet("broker_tls_ca") {
			crt, err := ioutil.ReadFile(ctx.String("broker_tls_ca"))
			if err != nil {
				logger.Fatalf("Error loading broker TLS certificate authority: %v", err)
			}
			caCertPool.AppendCertsFromPEM(crt)
		}

		cfg := &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: caCertPool}
		brokerOpts = append(brokerOpts, broker.TLSConfig(cfg))
	}

	// Setup registry options
	registryOpts := []registry.Option{registrySrv.WithClient(microClient)}

	// Parse registry TLS certs
	if ctx.IsSet("registry_tls_cert") || ctx.IsSet("registry_tls_key") {
		cert, err := tls.LoadX509KeyPair(ctx.String("registry_tls_cert"), ctx.String("registry_tls_key"))
		if err != nil {
			logger.Fatalf("Error loading registry tls cert: %v", err)
		}

		// load custom certificate authority
		caCertPool := x509.NewCertPool()
		if ctx.IsSet("registry_tls_ca") {
			crt, err := ioutil.ReadFile(ctx.String("registry_tls_ca"))
			if err != nil {
				logger.Fatalf("Error loading registry tls certificate authority: %v", err)
			}
			caCertPool.AppendCertsFromPEM(crt)
		}

		cfg := &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: caCertPool}
		registryOpts = append(registryOpts, registry.TLSConfig(cfg))
	}

	if len(ctx.String("registry_address")) > 0 {
		addresses := strings.Split(ctx.String("registry_address"), ",")
		registryOpts = append(registryOpts, registry.Addrs(addresses...))
	}

	// Set the registry
	if name := ctx.String("registry"); len(name) > 0 && (*c.opts.Registry).String() != name {
		r, ok := c.opts.Registries[name]
		if !ok {
			logger.Fatalf("Registry %s not found", name)
		}

		*c.opts.Registry = r(registryOpts...)
		serverOpts = append(serverOpts, server.Registry(*c.opts.Registry))
		brokerOpts = append(brokerOpts, broker.Registry(*c.opts.Registry))
	} else if len(registryOpts) > 0 {
		if err := (*c.opts.Registry).Init(registryOpts...); err != nil {
			logger.Fatalf("Error configuring registry: %v", err)
		}
	}

	// Set the selector
	if name := ctx.String("selector"); len(name) > 0 && (*c.opts.Selector).String() != name {
		s, ok := c.opts.Selectors[name]
		if !ok {
			logger.Fatalf("Selector %s not found", name)
		}

		*c.opts.Selector = s()
		clientOpts = append(clientOpts, client.Selector(*c.opts.Selector))
	}

	// Set the router, this must happen before the rest of the server as it'll route server requests
	// such as go.micro.config if no address is specified
	routerOpts := []router.Option{
		srvRouter.Client(microClient),
		router.Network(ctx.String("service_namespace")),
		router.Registry(*c.opts.Registry),
		router.Id((*c.opts.Server).Options().Id),
	}
	if len(ctx.String("router_address")) > 0 {
		routerOpts = append(routerOpts, router.Address(ctx.String("router_address")))
	}
	if name := ctx.String("router"); len(name) > 0 && (*c.opts.Router).String() != name {
		r, ok := c.opts.Routers[name]
		if !ok {
			logger.Fatalf("Router %s not found", name)
		}

		// close the default router before replacing it
		if err := (*c.opts.Router).Close(); err != nil {
			logger.Fatalf("Error closing default router: %s", name)
		}

		*c.opts.Router = r(routerOpts...)
		clientOpts = append(clientOpts, client.Router(*c.opts.Router))
	} else if len(routerOpts) > 0 {
		if err := (*c.opts.Router).Init(routerOpts...); err != nil {
			logger.Fatalf("Error configuring router: %v", err)
		}
	}

	// Setup store options
	storeOpts := []store.Option{store.WithClient(microClient)}
	if len(ctx.String("store_address")) > 0 {
		storeOpts = append(storeOpts, store.Nodes(strings.Split(ctx.String("store_address"), ",")...))
	}
	if len(ctx.String("store_database")) > 0 {
		storeOpts = append(storeOpts, store.Database(ctx.String("store_database")))
	} else if len(ctx.String("service_namespace")) > 0 {
		storeOpts = append(storeOpts, store.Database(ctx.String("service_namespace")))
	}
	if len(ctx.String("store_table")) > 0 {
		storeOpts = append(storeOpts, store.Table(ctx.String("store_table")))
	}

	// Set the store
	if name := ctx.String("store"); len(name) > 0 {
		s, ok := c.opts.Stores[name]
		if !ok {
			logger.Fatalf("Unsupported store: %s", name)
		}

		*c.opts.Store = s(storeOpts...)
	} else if len(storeOpts) > 0 {
		if err := (*c.opts.Store).Init(storeOpts...); err != nil {
			logger.Fatalf("Error configuring store: %v", err)
		}
	}

	// Setup the runtime options
	runtimeOpts := []runtime.Option{runtime.WithClient(microClient)}
	if len(ctx.String("runtime_source")) > 0 {
		runtimeOpts = append(runtimeOpts, runtime.WithSource(ctx.String("runtime_source")))
	}

	// Set the runtime
	if name := ctx.String("runtime"); len(name) > 0 {
		r, ok := c.opts.Runtimes[name]
		if !ok {
			logger.Fatalf("Unsupported runtime: %s", name)
		}

		*c.opts.Runtime = r(runtimeOpts...)
	} else if len(runtimeOpts) > 0 {
		if err := (*c.opts.Runtime).Init(runtimeOpts...); err != nil {
			logger.Fatalf("Error configuring runtime: %v", err)
		}
	}

	// Set the tracer
	if name := ctx.String("tracer"); len(name) > 0 {
		r, ok := c.opts.Tracers[name]
		if !ok {
			logger.Fatalf("Unsupported tracer: %s", name)
		}

		*c.opts.Tracer = r()
	}

	// Set the profile
	if name := ctx.String("profile"); len(name) > 0 {
		p, ok := c.opts.Profiles[name]
		if !ok {
			logger.Fatalf("Unsupported profile: %s", name)
		}

		*c.opts.Profile = p()
	}

	// Set the broker
	if name := ctx.String("broker"); len(name) > 0 && (*c.opts.Broker).String() != name {
		b, ok := c.opts.Brokers[name]
		if !ok {
			logger.Fatalf("Broker %s not found", name)
		}

		*c.opts.Broker = b(brokerOpts...)
		serverOpts = append(serverOpts, server.Broker(*c.opts.Broker))
		clientOpts = append(clientOpts, client.Broker(*c.opts.Broker))
	} else if len(brokerOpts) > 0 {
		if err := (*c.opts.Broker).Init(brokerOpts...); err != nil {
			logger.Fatalf("Error configuring broker: %v", err)
		}
	}

	// Setup the transport options
	var transportOpts []transport.Option
	if len(ctx.String("transport_address")) > 0 {
		addresses := strings.Split(ctx.String("transport_address"), ",")
		transportOpts = append(transportOpts, transport.Addrs(addresses...))
	}

	// Set the transport
	if name := ctx.String("transport"); len(name) > 0 && (*c.opts.Transport).String() != name {
		t, ok := c.opts.Transports[name]
		if !ok {
			logger.Fatalf("Transport %s not found", name)
		}

		*c.opts.Transport = t(transportOpts...)
		serverOpts = append(serverOpts, server.Transport(*c.opts.Transport))
		clientOpts = append(clientOpts, client.Transport(*c.opts.Transport))
	} else if len(transportOpts) > 0 {
		if err := (*c.opts.Transport).Init(transportOpts...); err != nil {
			logger.Fatalf("Error configuring transport: %v", err)
		}
	}

	// Setup config sources
	if ctx.String("config") == "service" {
		opt := config.WithSource(configSrv.NewSource(
			configSrc.WithClient(microClient),
			configSrv.Namespace(ctx.String("service_namespace")),
		))

		if err := (*c.opts.Config).Init(opt); err != nil {
			if logger.V(logger.DebugLevel, logger.DefaultLogger) {
				logger.Debugf("Error configuring config: %v", err)
			}
		}
	}

	// Set the client
	if name := ctx.String("client"); len(name) > 0 && (*c.opts.Client).String() != name {
		cl, ok := c.opts.Clients[name]
		if !ok {
			logger.Fatalf("Client %s not found", name)
		}

		*c.opts.Client = cl(clientOpts...)
	} else if len(clientOpts) > 0 {
		if err := (*c.opts.Client).Init(clientOpts...); err != nil {
			logger.Fatalf("Error configuring client: %v", err)
		}
	}

	// Set the server
	if name := ctx.String("server"); len(name) > 0 && (*c.opts.Server).String() != name {
		s, ok := c.opts.Servers[name]
		if !ok {
			logger.Fatalf("Server %s not found", name)
		}

		*c.opts.Server = s(serverOpts...)
	} else if len(serverOpts) > 0 {
		if err := (*c.opts.Server).Init(serverOpts...); err != nil {
			logger.Fatalf("Error configuring server: %v", err)
		}
	}

	return nil
}

func (c *cmd) Init(opts ...Option) error {
	for _, o := range opts {
		o(&c.opts)
	}
	if len(c.opts.Name) > 0 {
		c.app.Name = c.opts.Name
	}
	if len(c.opts.Version) > 0 {
		c.app.Version = c.opts.Version
	}
	c.app.HideVersion = len(c.opts.Version) == 0
	c.app.Usage = c.opts.Description
	return nil
}

func (c *cmd) Run() error {
	return c.app.Run(os.Args)
}

func (c *cmd) String() string {
	return "micro/cli"
}

func App() *cli.App {
	return DefaultCmd.App()
}

func Init(opts ...Option) error {
	return DefaultCmd.Init(opts...)
}

func NewCmd(opts ...Option) Cmd {
	return newCmd(opts...)
}

// Run the default command
func Run() error {
	return DefaultCmd.Run()
}
