package combainer

import (
	"crypto/md5"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/cocaine/cocaine-framework-go/cocaine"
	"github.com/howeyc/fsnotify"
	"launchpad.net/goyaml"

	"github.com/noxiouz/Combaine/common"
)

type combainerMainCfg struct {
	Http_hand     string "HTTP_HAND"
	MinimumPeriod uint   "MINIMUM_PERIOD"
	CloudHosts    string "cloud"
}

type combainerLockserverCfg struct {
	Id      string   "app_id"
	Hosts   []string "host"
	Name    string   "name"
	timeout uint     "timeout"
}

type combainerConfig struct {
	Combainer struct {
		Main          combainerMainCfg       "Main"
		LockServerCfg combainerLockserverCfg "Lockserver"
	} "Combainer"
}

type sessionParams struct {
	ParsingTime time.Duration
	WholeTime   time.Duration
	PTasks      []common.ParsingTask
	AggTasks    []common.AggregationTask
}

type clientStats struct {
	sync.RWMutex
	successParsing   int
	failedParsing    int
	successAggregate int
	failedAggregate  int
	last             int64
}

type Client struct {
	Main       combainerMainCfg
	LSCfg      combainerLockserverCfg
	DLS        LockServer
	lockname   string
	cloudHosts []string
	clientStats

	// various periods and list of tasks
	sp *sessionParams
}

func (cs *clientStats) AddSuccessParsing() {
	cs.Lock()
	cs.successParsing++
	cs.last = time.Now().Unix()
	cs.Unlock()
}

func (cs *clientStats) AddFailedParsing() {
	cs.Lock()
	cs.failedParsing++
	cs.last = time.Now().Unix()
	cs.Unlock()
}

func (cs *clientStats) AddSuccessAggregate() {
	cs.Lock()
	cs.successAggregate++
	cs.last = time.Now().Unix()
	cs.Unlock()
}

func (cs *clientStats) AddFailedAggregate() {
	cs.Lock()
	cs.failedAggregate++
	cs.last = time.Now().Unix()
	cs.Unlock()
}

func (cs *clientStats) GetStats() (info *StatInfo) {
	cs.RLock()
	// var success = cs.success
	// var failed = cs.failed
	defer cs.RUnlock()
	info = &StatInfo{
		ParsingSuccess:   cs.successParsing,
		ParsingFailed:    cs.failedParsing,
		ParsingTotal:     cs.successParsing + cs.failedParsing,
		AggregateSuccess: cs.successAggregate,
		AggregateFailed:  cs.failedAggregate,
		AggregateTotal:   cs.successAggregate + cs.failedAggregate,
		Heartbeated:      cs.last,
	}
	return
}

// Public API

func NewClient(config string) (*Client, error) {
	// Read combaine.yaml
	data, err := ioutil.ReadFile(config)
	if err != nil {
		return nil, err
	}

	// Parse combaine.yaml
	var m combainerConfig
	err = goyaml.Unmarshal(data, &m)
	if err != nil {
		return nil, err
	}

	// Zookeeper hosts. Connect to Zookeeper
	hosts := m.Combainer.LockServerCfg.Hosts
	dls, err := NewLockServer(strings.Join(hosts, ","))
	if err != nil {
		return nil, err
	}

	cloudHosts, err := GetHosts(m.Combainer.Main.Http_hand, m.Combainer.Main.CloudHosts)
	if err != nil {
		return nil, err
	}
	return &Client{
		Main:       m.Combainer.Main,
		LSCfg:      m.Combainer.LockServerCfg,
		DLS:        *dls,
		lockname:   "",
		cloudHosts: cloudHosts,
		sp:         nil,
	}, nil
}

func (cl *Client) Close() {
	cl.DLS.Close()
}

func (cl *Client) UpdateSessionParams(config string) (err error) {
	LogInfo("Updating session parametrs")
	// tasks
	var p_tasks []common.ParsingTask
	var agg_tasks []common.AggregationTask

	// timeouts
	var parsingTime time.Duration
	var wholeTime time.Duration

	res, err := loadConfig(cl.lockname)
	if err != nil {
		LogErr("Unable to load config %s", err)
		return
	}

	if res.MinimumPeriod > 0 {
		cl.Main.MinimumPeriod = res.MinimumPeriod
	}

	var metahost string
	if len(res.Metahost) != 0 {
		metahost = res.Metahost
	} else {
		metahost = res.Groups[0]
	}
	LogInfo("Metahost %s", metahost)
	// Make list of hosts
	var hosts []string
	for _, item := range res.Groups {
		if hosts_for_group, err := GetHosts(cl.Main.Http_hand, item); err != nil {
			LogInfo("Item %s, err %s", item, err)
		} else {
			hosts = append(hosts, hosts_for_group...)
		}
		LogInfo("Hosts: %s", hosts)
	}

	// Tasks for parsing
	//host_name, config_name, group_name, previous_time, current_time
	for _, host := range hosts {
		p_tasks = append(p_tasks, common.ParsingTask{
			Host:     host,
			Config:   cl.lockname,
			Group:    res.Groups[0],
			PrevTime: -1,
			CurrTime: -1,
			Id:       "",
			Metahost: metahost,
		})
	}

	//groupname, config_name, agg_config_name, previous_time, current_time
	for _, cfg := range res.AggConfigs {
		agg_tasks = append(agg_tasks, common.AggregationTask{
			Config:   cfg,
			PConfig:  cl.lockname,
			Group:    res.Groups[0],
			PrevTime: -1,
			CurrTime: -1,
			Id:       "",
			Metahost: metahost,
		})
	}

	parsingTime = time.Duration(float64(cl.Main.MinimumPeriod)*0.8) * time.Second
	wholeTime = time.Duration(cl.Main.MinimumPeriod) * time.Second

	sp := sessionParams{
		ParsingTime: parsingTime,
		WholeTime:   wholeTime,
		PTasks:      p_tasks,
		AggTasks:    agg_tasks,
	}

	LogInfo("Session parametrs have been updated successfully. %v", sp)
	cl.sp = &sp
	return nil
}

func (cl *Client) Dispatch() {
	defer cl.Close()

	lockpoller := cl.acquireLock()
	if lockpoller != nil {
		LogInfo("Acquire Lock %s", cl.lockname)
	} else {
		return
	}

	// Create inotify filewatcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		LogErr("Unable to create filewatcher %s", err)
		return
	}
	defer watcher.Close()

	// Start watching configuration file
	configFile := fmt.Sprintf("%s%s", CONFIGS_PARSING_PATH, cl.lockname)
	err = watcher.Watch(configFile)
	if err != nil {
		LogErr("Unable to watch file %s %s", configFile, err)
		return
	}

	_observer.RegisterClient(cl, cl.lockname)
	defer _observer.UnregisterClient(cl.lockname)

	// Dispatch
	var deadline time.Time
	var startTime time.Time
	var wg sync.WaitGroup

	for {
		//Update session parametrs from config
		if err := cl.UpdateSessionParams(cl.lockname); err != nil {
			LogInfo("Error %s", err)
			return
		}

		if cl.sp == nil {
			LogInfo("Unable to update parametrs of session")
			return
		}

		// Start periodically
		startTime = time.Now()
		deadline = startTime.Add(cl.sp.ParsingTime)

		// Generate session unique ID
		h := md5.New()
		io.WriteString(h, (fmt.Sprintf("%s%d%d", cl.lockname, startTime, deadline)))
		uniqueID := fmt.Sprintf("%x", h.Sum(nil))
		LogInfo("%s Start new iteration.", uniqueID)

		// Parsing phase
		for i, task := range cl.sp.PTasks {
			// Description of task
			task.PrevTime = startTime.Unix()
			task.CurrTime = startTime.Add(cl.sp.WholeTime).Unix()
			task.Id = uniqueID

			LogInfo("%s Send task number %d to parsing %v", uniqueID, i+1, task)
			wg.Add(1)
			go cl.parsingTaskHandler(task, &wg, deadline)
		}
		wg.Wait()
		LogInfo("%s Parsing finished", uniqueID)

		// Aggregation phase
		deadline = startTime.Add(cl.sp.WholeTime)
		for i, task := range cl.sp.AggTasks {
			task.PrevTime = startTime.Unix()
			task.CurrTime = startTime.Add(cl.sp.WholeTime).Unix()
			task.Id = uniqueID
			LogInfo("%s Send task number %d to aggregate %v", uniqueID, i+1, task)
			wg.Add(1)
			go cl.aggregationTaskHandler(task, &wg, deadline)
		}
		wg.Wait()
		LogInfo("%s Aggregation finished", uniqueID)

		select {
		// Does lock exist?
		case <-lockpoller: // Lock
			LogInfo("%s Drop lock %s", uniqueID, cl.lockname)
			return

		// Wait for next iteration
		case <-time.After(deadline.Sub(time.Now())):

		// Handle possible configuration updates
		case ev := <-watcher.Event:
			// It looks like a bug, but opening file in vim emits rename event
			if ev.IsModify() || ev.IsRename() {

				// Does file exist with the same name still?
				if ev.Name != configFile {
					LogErr("%s File has been renamed to %s. Drop lock %s", uniqueID, ev.Name, cl.lockname)
					return
				}

				LogInfo("%s %s has been changed. Updating configuration", uniqueID, cl.lockname)
				if err = cl.UpdateSessionParams(cl.lockname); err != nil {
					LogErr("%s Unable to update configuration %s. Drop lock %s", uniqueID, err, cl.lockname)
					return
				}
				LogInfo("%s Configuration %s has been updated successfully", uniqueID, cl.lockname)

				<-time.After(deadline.Sub(time.Now()))

			} else if ev.IsDelete() {
				LogInfo("%s %s has been removed. Drop lock", uniqueID, cl.lockname)
				return
			} else if ev.IsCreate() {
				LogErr("%s Assertation error. Config %s has been created", uniqueID, cl.lockname)
				return
			}

		// Handle configuration watcher error
		case err = <-watcher.Error:
			LogErr("%s Watcher error %s. Drop lock %s", uniqueID, err, cl.lockname)
			return
		}
		LogInfo("%s Go to the next iteration", uniqueID)
	}
}

//----------------
type ResolveInfo struct {
	App *cocaine.Service
	Err error
}

func Resolve(appname, endpoint string) <-chan ResolveInfo {
	res := make(chan ResolveInfo, 1)
	go func() {
		app, err := cocaine.NewService(appname, endpoint)
		select {
		case res <- ResolveInfo{
			App: app,
			Err: err,
		}:
		default:
			if err == nil {
				app.Close()
			}
		}
	}()
	return res
}

//------------------

func (cl *Client) parsingTaskHandler(task common.ParsingTask, wg *sync.WaitGroup, deadline time.Time) {
	defer (*wg).Done()
	limit := deadline.Sub(time.Now())

	var app *cocaine.Service
	var err error
	for deadline.After(time.Now()) {
		host := fmt.Sprintf("%s:10053", cl.getRandomHost())
		// app, err = cocaine.NewService(common.PARSING, host)
		select {
		case r := <-Resolve(common.PARSING, host):
			err = r.Err
			app = r.App
		case <-time.After(1 * time.Second):
			err = fmt.Errorf("service resolvation was timeouted %s %s %s", task.Id, host, common.PARSING)
		}
		if err == nil {
			defer app.Close()
			LogDebug("%s Host: %s", task.Id, host)
			break
		} else {
			LogWarning("%s unable to connect to application %s %s %s", task.Id, common.PARSING, host, err)
		}
		time.Sleep(200 * time.Microsecond)
	}

	if app == nil {
		LogErr("Unable to send task %s. Application is unavailable", task.Id)
		cl.clientStats.AddFailedParsing()
		return
	}

	raw, _ := common.Pack(task)
	select {
	case <-time.After(limit):
		LogErr("Task %s has been late\n", task.Id)
		cl.clientStats.AddFailedParsing()
	case res := <-app.Call("enqueue", "handleTask", raw):
		if res.Err() != nil {
			LogErr("%s Parsing task for host %s failed %v", task.Id, task.Host, res.Err())
		} else {
			LogInfo("%s Parsing task for host %s completed successfully", task.Id, task.Host)
		}
		cl.clientStats.AddSuccessParsing()
	}
}

func (cl *Client) aggregationTaskHandler(task common.AggregationTask, wg *sync.WaitGroup, deadline time.Time) {
	defer (*wg).Done()
	limit := deadline.Sub(time.Now())

	var app *cocaine.Service
	var err error
	for deadline.After(time.Now()) {
		host := fmt.Sprintf("%s:10053", cl.getRandomHost())
		select {
		case r := <-Resolve(common.AGGREGATE, host):
			err = r.Err
			app = r.App
		case <-time.After(1 * time.Second):
			err = fmt.Errorf("service resolvation was timeouted %s %s %s", task.Id, host, common.AGGREGATE)
		}
		if err == nil {
			defer app.Close()
			LogDebug("%s Host: %s", task.Id, host)
			break
		} else {
			LogWarning("%s unable to connect to application %s %s %s", task.Id, common.AGGREGATE, host, err)
		}
		time.Sleep(time.Millisecond * 100)
	}

	if app == nil {
		cl.clientStats.AddFailedAggregate()
		LogErr("Unable to send aggregate task %s. Application is unavailable", task.Id)
		return
	}

	raw, _ := common.Pack(task)
	select {
	case <-time.After(limit):
		LogErr("Task %s has been late", task.Id)
		cl.clientStats.AddFailedAggregate()
	case res := <-app.Call("enqueue", "handleTask", raw):
		if res.Err() != nil {
			LogErr("%s Aggreagation task for group %s failed %v", task.Id, task.Group, res.Err())
		} else {
			LogInfo("%s Aggregation task for group %s completed successfully", task.Id, task.Group)
		}
		cl.clientStats.AddSuccessAggregate()
	}
}

func (cl *Client) getRandomHost() string {
	max := len(cl.cloudHosts)
	return cl.cloudHosts[rand.Intn(max)]
}

// Private API
func (cl *Client) acquireLock() chan bool {
	for _, i := range getParsings() {
		lockname := fmt.Sprintf("/%s/%s", cl.LSCfg.Id, i)
		poller := cl.DLS.AcquireLock(lockname)
		if poller != nil {
			cl.lockname = i
			return poller
		}
	}
	return nil
}
