package graphite

import (
	"fmt"
	"net"
	"reflect"
	"strings"
	"time"

	"github.com/noxiouz/Combaine/common"
	"github.com/noxiouz/Combaine/common/logger"
)

func formatSubgroup(input string) string {
	return strings.Replace(
		strings.Replace(input, ".", "_", -1),
		"-", "_", -1)
}

const onePointTemplateText = "{{.cluster}}.combaine.{{.metahost}}.{{.item}} {{.value}} {{.timestamp}}"

const onePointFormat = "%s.combaine.%s.%s %s %d\n"

// var onePointTemplate *template.Template = template.Must(template.New("URL").Parse(onePointTemplateText))

type GraphiteSender interface {
	Send(common.DataType) error
}

const connectionTimeout = 900       //msec
const connectionEndpoint = ":42000" //msec

type graphiteClient struct {
	id      string
	cluster string
	fields  []string
}

type GraphiteCfg struct {
	Cluster string   `codec:"cluster"`
	Fields  []string `codec:"Fields"`
}

/*
common.DataType:
{
	"20x": {
		"group1": 2000,
		"group2": [20, 30, 40]
	},
}
*/

func (g *graphiteClient) Send(data common.DataType) (err error) {
	if len(data) == 0 {
		return fmt.Errorf("%s Empty data. Nothing to send.", g.id)
	}

	sock, err := net.DialTimeout("tcp", connectionEndpoint, time.Microsecond*connectionTimeout)
	if err != nil {
		logger.Errf("Unable to connect to daemon %s: %s", connectionEndpoint, err)
		return
	}
	defer sock.Close()

	for aggname, subgroupsAndValues := range data {
		logger.Debugf("%s Handle aggregate named %s", g.id, aggname)
	SUBGROUPS_AND_VALUES:
		for subgroup, value := range subgroupsAndValues {
			rv := reflect.ValueOf(value)
			switch kind := rv.Kind(); kind {
			case reflect.Slice, reflect.Array:
				if len(g.fields) == 0 || len(g.fields) != rv.Len() {
					logger.Errf("%s Unable to send a slice. Fields len %d, len of value %d", g.id, len(g.fields), rv.Len())
					continue SUBGROUPS_AND_VALUES
				}
				for i := 0; i < rv.Len(); i++ {
					itemInterface := rv.Index(i).Interface()
					toSend := fmt.Sprintf(
						onePointFormat,
						g.cluster,
						formatSubgroup(subgroup),
						fmt.Sprintf("%s.%s", aggname, g.fields[i]),
						common.InterfaceToString(itemInterface),
						time.Now().Unix())

					logger.Infof("%s Send %s", g.id, toSend)
					if _, err = fmt.Fprint(sock, toSend); err != nil {
						logger.Errf("%s Sending error: %s", g.id, err)
						return err
					}
				}
			case reflect.Map:
				// It must be recursive algorithm
				v_keys := rv.MapKeys()
				for _, key := range v_keys {
					itemInterface := rv.MapIndex(key)

					switch itemInterface.Kind() {
					case reflect.Slice, reflect.Array:
						if len(g.fields) == 0 || len(g.fields) != itemInterface.Len() {
							logger.Errf("%s Unable to send a slice. Fields len %d, len of value %d", g.id, len(g.fields), itemInterface.Len())
						}
						for i := 0; i < itemInterface.Len(); i++ {
							itemInnerInterface := itemInterface.Index(i).Interface()
							toSend := fmt.Sprintf(
								onePointFormat,
								g.cluster,
								formatSubgroup(subgroup),
								fmt.Sprintf("%s.%s.%s", aggname, common.InterfaceToString(key.Interface()), g.fields[i]),
								common.InterfaceToString(itemInnerInterface),
								time.Now().Unix())

							logger.Infof("%s Send %s", g.id, toSend)
							if _, err = fmt.Fprint(sock, toSend); err != nil {
								logger.Errf("%s Sending error: %s", g.id, err)
								return err
							}
						}
					default:
						toSend := fmt.Sprintf(
							onePointFormat,
							g.cluster,
							formatSubgroup(subgroup),
							fmt.Sprintf("%s.%s", aggname, common.InterfaceToString(key.Interface())),
							common.InterfaceToString(itemInterface.Interface()),
							time.Now().Unix())

						logger.Infof("%s Send %s", g.id, toSend)
						if _, err = fmt.Fprint(sock, toSend); err != nil {
							logger.Errf("%s Sending error: %s", g.id, err)
							return err
						}
					}
				}

			default:
				toSend := fmt.Sprintf(
					onePointFormat,
					g.cluster,
					formatSubgroup(subgroup),
					aggname,
					common.InterfaceToString(value),
					time.Now().Unix())

				logger.Infof("%s Send %s", g.id, toSend)
				if _, err = fmt.Fprint(sock, toSend); err != nil {
					logger.Errf("%s Sending error: %s", g.id, err)
					return err
				}
			}
		}

	}
	return
}

func NewGraphiteClient(cfg *GraphiteCfg, id string) (gs GraphiteSender, err error) {
	gs = &graphiteClient{
		id:      id,
		cluster: cfg.Cluster,
		fields:  cfg.Fields,
	}

	return
}
