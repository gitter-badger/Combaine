package combainer

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/cocaine/cocaine-framework-go/cocaine"

	"github.com/noxiouz/Combaine/common"
	"github.com/noxiouz/Combaine/common/configs"
	"github.com/noxiouz/Combaine/common/hosts"
	"github.com/noxiouz/Combaine/common/tasks"
)

type sessionParams struct {
	ParsingTime time.Duration
	WholeTime   time.Duration
	PTasks      []tasks.ParsingTask
	AggTasks    []tasks.AggregationTask
}

type Client struct {
	Repository configs.Repository
	Config     *configs.CombainerConfig
	cloudHosts []string
	*Context
	clientStats
}

func NewClient(context *Context, config *configs.CombainerConfig, repo configs.Repository) (*Client, error) {
	// Zookeeper hosts. Connect to Zookeeper
	// TBD: It's better to pass config as is of create lockserver outside of client
	// dls, err := NewLockServer(strings.Join(config.LockServerSection.Hosts, ","))
	// if err != nil {
	// 	return nil, err
	// }

	s, err := NewSimpleFetcher(context, config.CloudSection.HostFetcher)
	if err != nil {
		return nil, err
	}

	// Get Combaine hosts
	cloudHosts, err := s.Fetch(config.MainSection.CloudGroup)
	if err != nil {
		return nil, err
	}

	cl := &Client{
		Repository: repo,
		Config:     config,
		cloudHosts: cloudHosts.AllHosts(),
		Context:    context,
	}

	return cl, nil
}

func (cl *Client) UpdateSessionParams(config string) (sp *sessionParams, err error) {
	LogInfo("Updating session parametrs")
	var (
		// tasks
		p_tasks   []tasks.ParsingTask
		agg_tasks []tasks.AggregationTask

		// timeouts
		parsingTime time.Duration
		wholeTime   time.Duration
	)

	encodedParsingConfig, err := cl.Repository.GetParsingConfig(config)
	if err != nil {
		LogErr("Unable to load config %s", err)
		return nil, err
	}

	var parsingConfig configs.ParsingConfig
	if err := encodedParsingConfig.Decode(&parsingConfig); err != nil {
		LogErr("Unable to decode parsingConfig: %s", err)
		return nil, err
	}

	parsingConfig.UpdateByCombainerConfig(cl.Config)
	aggregationConfigs, err := GetAggregationConfigs(cl.Repository, &parsingConfig)
	if err != nil {
		LogErr("Unable to read aggregation configs: %s", err)
		return nil, err
	}

	LogInfo("Updating config: group %s, metahost %s",
		parsingConfig.GetGroup(), parsingConfig.GetMetahost())

	hostFetcher, err := NewSimpleFetcher(cl.Context, parsingConfig.HostFetcher)
	if err != nil {
		LogErr("Unable to construct SimpleFetcher: %s", err)
		return
	}

	allHosts := make(hosts.Hosts)
	for _, item := range parsingConfig.Groups {
		hosts_for_group, err := hostFetcher.Fetch(item)
		if err != nil {
			LogInfo("Unable to get hosts for group %s: %s", item, err)
			continue
		}

		allHosts.Merge(&hosts_for_group)
	}
	listOfHosts := allHosts.AllHosts()
	LogInfo("Hosts: %s", listOfHosts)

	// Tasks for parsing
	for _, host := range listOfHosts {
		p_tasks = append(p_tasks, tasks.ParsingTask{
			CommonTask:         tasks.EmptyCommonTask,
			Host:               host,
			ParsingConfigName:  config,
			ParsingConfig:      parsingConfig,
			AggregationConfigs: *aggregationConfigs,
		})
	}

	for _, name := range parsingConfig.AggConfigs {
		agg_tasks = append(agg_tasks, tasks.AggregationTask{
			CommonTask:        tasks.EmptyCommonTask,
			Config:            name,
			ParsingConfigName: config,
			ParsingConfig:     parsingConfig,
			AggregationConfig: (*aggregationConfigs)[name],
			Hosts:             allHosts,
		})
	}

	parsingTime, wholeTime = GenerateSessionTimeFrame(parsingConfig.IterationDuration)

	sp = &sessionParams{
		ParsingTime: parsingTime,
		WholeTime:   wholeTime,
		PTasks:      p_tasks,
		AggTasks:    agg_tasks,
	}

	LogInfo("Session parametrs have been updated successfully. %v", sp)
	return sp, nil
}

func (cl *Client) Dispatch(parsingConfigName string) error {
	_observer.RegisterClient(cl, parsingConfigName)
	defer _observer.UnregisterClient(parsingConfigName)

	var deadline, startTime time.Time
	var wg sync.WaitGroup

	sessionParameters, err := cl.UpdateSessionParams(parsingConfigName)
	if err != nil {
		LogInfo("Error %s", err)
		return err
	}

	startTime = time.Now()
	deadline = startTime.Add(sessionParameters.ParsingTime)

	// Generate session unique ID
	uniqueID := GenerateSessionId(parsingConfigName, &startTime, &deadline)
	LogInfo("%s Start new iteration.", uniqueID)

	// Parsing phase
	for i, task := range sessionParameters.PTasks {
		// Description of task
		task.PrevTime = startTime.Unix()
		task.CurrTime = startTime.Add(sessionParameters.WholeTime).Unix()
		task.Id = uniqueID

		LogInfo("%s Send task number %d to parsing %v", uniqueID, i+1, task)
		wg.Add(1)
		go cl.parsingTaskHandler(task, &wg, deadline)
	}
	wg.Wait()
	LogInfo("%s Parsing finished", uniqueID)

	// Aggregation phase
	deadline = startTime.Add(sessionParameters.WholeTime)
	for i, task := range sessionParameters.AggTasks {
		task.PrevTime = startTime.Unix()
		task.CurrTime = startTime.Add(sessionParameters.WholeTime).Unix()
		task.Id = uniqueID
		LogInfo("%s Send task number %d to aggregate %v", uniqueID, i+1, task)
		wg.Add(1)
		go cl.aggregationTaskHandler(task, &wg, deadline)
	}
	wg.Wait()
	LogInfo("%s Aggregation finished", uniqueID)

	// Wait for next iteration
	time.Sleep(deadline.Sub(time.Now()))
	LogInfo("%s Go to the next iteration", uniqueID)
	return nil
}

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

func (cl *Client) parsingTaskHandler(task tasks.ParsingTask, wg *sync.WaitGroup, deadline time.Time) {
	defer (*wg).Done()
	limit := deadline.Sub(time.Now())

	var app *cocaine.Service
	var err error
	for deadline.After(time.Now()) {
		host := fmt.Sprintf("%s:10053", cl.getRandomHost())
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
		}

		LogWarning("%s unable to connect to application %s %s %s", task.Id, common.PARSING, host, err)
		time.Sleep(200 * time.Microsecond)
	}

	if app == nil {
		LogErr("Unable to send task %s. Application is unavailable", task.Id)
		cl.clientStats.AddFailedParsing()
		return
	}

	raw, _ := common.Pack(task)
	if res, err := PerformTask(app, raw, limit); err != nil {
		LogErr("%s Parsing task for group %s failed: %s", task.Id, task.ParsingConfig.GetGroup(), err)
		cl.clientStats.AddFailedParsing()
		return
	} else {
		LogInfo("%s Parsing task for group %s finished good: %s", task.Id, task.ParsingConfig.GetGroup(), res)
	}
	cl.clientStats.AddSuccessParsing()
}

func (cl *Client) aggregationTaskHandler(task tasks.AggregationTask, wg *sync.WaitGroup, deadline time.Time) {
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
		}

		LogWarning("%s unable to connect to application %s %s %s", task.Id, common.AGGREGATE, host, err)
		time.Sleep(time.Millisecond * 100)
	}

	if app == nil {
		cl.clientStats.AddFailedAggregate()
		LogErr("Unable to send aggregate task %s. Application is unavailable", task.Id)
		return
	}

	raw, _ := common.Pack(task)
	if res, err := PerformTask(app, raw, limit); err != nil {
		LogErr("%s Aggreagation task for group %s failed: %s", task.Id, task.ParsingConfig.GetGroup(), err)
		cl.clientStats.AddFailedAggregate()
		return
	} else {
		LogInfo("%s Aggreagation task for group %s finished good: %s", task.Id, task.ParsingConfig.GetGroup(), res)
	}
	cl.clientStats.AddSuccessAggregate()
}

func (cl *Client) getRandomHost() string {
	max := len(cl.cloudHosts)
	return cl.cloudHosts[rand.Intn(max)]
}

// Private API
// func (cl *Client) acquireLock() chan bool {
// 	parsingListing, _ := cl.Repository.ListParsingConfigs()
// 	for _, i := range parsingListing {
// 		lockname := fmt.Sprintf("/%s/%s", cl.Config.LockServerSection.Id, i)
// 		poller := cl.DLS.AcquireLock(lockname)
// 		if poller != nil {
// 			cl.lockname = i
// 			return poller
// 		}
// 	}
// 	return nil
// }

func PerformTask(app *cocaine.Service, payload []byte, limit time.Duration) (interface{}, error) {
	select {
	case <-time.After(limit):
		return nil, fmt.Errorf("timeout")
	case res := <-app.Call("enqueue", "handleTask", payload):
		if res.Err() != nil {
			return nil, res.Err()
		}
		var i interface{}
		err := res.Extract(&i)
		return i, err
	}
	return nil, nil
}
