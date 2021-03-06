package main

import (
	"flag"
	"log"
	"time"

	"launchpad.net/gozk/zookeeper"

	"github.com/noxiouz/Combaine/combainer"
	"github.com/noxiouz/Combaine/common/cache"
	"github.com/noxiouz/Combaine/common/configs"
)

const (
	CONFIGS_PATH   = "/etc/combaine"
	DEFAULT_PERIOD = 5
)

var (
	endpoint    string
	profiler    string
	logoutput   string
	loglevel    string
	ConfigsPath string
	period      uint
)

func init() {
	flag.StringVar(&endpoint, "observer", "0.0.0.0:9000", "HTTP observer port")
	flag.StringVar(&logoutput, "logoutput", "/dev/stderr", "path to logfile")
	flag.StringVar(&loglevel, "loglevel", "INFO", "loglevel (DEBUG|INFO|WARN|ERROR)")
	flag.StringVar(&ConfigsPath, "configspath", CONFIGS_PATH, "path to root of configs")
	flag.UintVar(&period, "period", 5, "period of retrying new lock (sec)")
}

func Trap() {
	if r := recover(); r != nil {
		log.Printf("Recovered: %s", r)
	}
}

type CombaineServer struct {
	Configuration   CombaineServerConfig
	CombainerConfig configs.CombainerConfig

	configs.Repository
	cache.Cache
	*combainer.Context
}

type CombaineServerConfig struct {
	// Configuration
	// path to directory with combaine.yaml
	ConfigsPath string
	// period of the locks rechecking
	Period time.Duration
	// Addrto listen for incoming http REST API requests
	RestEndpoint string
}

func NewCombainer(config CombaineServerConfig) (*CombaineServer, error) {
	repository, err := configs.NewFilesystemRepository(config.ConfigsPath)
	if err != nil {
		log.Fatalf("unable to initialize filesystemRepository: %s", err)
	}

	combainerConfig := repository.GetCombainerConfig()

	cacheCfg := &combainerConfig.MainSection.Cache
	cacheType, err := cacheCfg.Type()
	if err != nil {
		log.Fatalf("unable to get type of cache: %s", err)
	}

	cacher, err := cache.NewCache(cacheType, cacheCfg)
	if err != nil {
		log.Fatalf("unable to initialize cache: %s", err)
	}

	context := &combainer.Context{
		Cache: cacher,
	}

	server := &CombaineServer{
		Configuration:   config,
		CombainerConfig: combainerConfig,
		Repository:      repository,
		Cache:           cacher,
		Context:         context,
	}

	return server, nil
}

func (c *CombaineServer) Serve() error {
	log.Println("Starting REST API")
	go combainer.StartObserver(c.Configuration.RestEndpoint, c.Repository)
LOCKSERVER_LOOP:
	for {
		DLS, err := combainer.NewLockServer(c.CombainerConfig.LockServerSection)
		if err != nil {
			log.Printf("Unable to create Zookeeper lockserver: %s", err)
			time.Sleep(c.Configuration.Period)
			continue LOCKSERVER_LOOP
		}

		var next <-chan time.Time
		next = time.After(time.Millisecond * 10)

	DISPATCH_LOOP:
		for {
			select {
			// Spawn one more client
			case <-next:
				next = time.After(c.Configuration.Period)
				configs, err := c.Repository.ListParsingConfigs()
				if err != nil {
					log.Printf("Unable to get list of parsing configs: %s", err)
					continue DISPATCH_LOOP
				}

				var lockname string
				var lockerr error
				for _, cfg := range configs {
					lockerr = DLS.Lock(cfg)
					if lockerr == nil {
						lockname = cfg
						break
					}
				}

				if lockerr != nil {
					log.Printf("Unable to get any freelock: %s", lockerr)
					continue DISPATCH_LOOP
				}

				go func(lockname string) {
					defer DLS.Unlock(lockname)
					defer Trap()

					log.Printf("Creating new client with lock: %s", lockname)
					cl, err := combainer.NewClient(c.Context, &c.CombainerConfig, c.Repository)
					if err != nil {
						log.Printf("Can't create client: %s", err)
						return
					}

					var watcher <-chan zookeeper.Event
					watcher, err = DLS.Watch(lockname)
					if err != nil {
						log.Printf("Can't watch %s: %s", lockname, err)
						return
					}

					for {
						if err := cl.Dispatch(lockname); err != nil {
							log.Println("Dispatch error: %s", err)
							return
						}
						select {
						case event := <-watcher:
							if !event.Ok() || event.Type == zookeeper.EVENT_DELETED {
								log.Println("lock has been lost: %s", event)
								return
							}
							watcher, err = DLS.Watch(lockname)
							if err != nil {
								log.Printf("Can't watch %s: %s", lockname, err)
								return
							}
						default:
						}
					}
				}(lockname)
			case event := <-DLS.Session:
				if !event.Ok() {
					log.Printf("Not OK event from Zookeeper: %s", event)
					DLS.Close()
					break DISPATCH_LOOP
				}
			}

		}
	}

	return nil
}

func main() {
	flag.Parse()
	combainer.InitializeLogger(loglevel, logoutput)
	cfg := CombaineServerConfig{
		ConfigsPath:  ConfigsPath,
		Period:       time.Duration(period) * time.Second,
		RestEndpoint: endpoint,
	}

	cmb, err := NewCombainer(cfg)
	if err != nil {
		log.Fatal(err)
	}

	cmb.Serve()
}
