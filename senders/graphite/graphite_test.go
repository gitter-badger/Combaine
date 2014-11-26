package graphite

import (
	"fmt"
	"io/ioutil"
	"net"
	"testing"
	"time"

	"github.com/noxiouz/Combaine/common"
)

func z() {
	handleConnection := func(conn net.Conn) {
		d, _ := ioutil.ReadAll(conn)
		fmt.Printf("%s", d)
	}

	ln, err := net.Listen("tcp", ":42000")
	if err != nil {
		return
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go handleConnection(conn)
	}
}

func TestMain(t *testing.T) {
	go z()
	time.Sleep(1 * time.Second)
	grCfg := graphiteClient{
		id:      "TESTID",
		cluster: "TESTCOMBAINE",
		fields:  []string{"A", "B", "C"},
	}

	data := common.DataType{
		"20x": {
			"simple": 2000,
			"array":  []int{20, 30, 40},
			"map_of_array": map[string][]int{
				"MAP1": []int{201, 301, 401},
				"MAP2": []int{202, 302, 402},
			},
			"map_of_simple": map[string]int{
				"MP1": 1000,
				"MP2": 1002,
			},
		}}

	t.Log(grCfg)
	err := grCfg.Send(data)
	time.Sleep(1 * time.Second)
	t.Log(err)
}
