package configs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUtilityFunctions(t *testing.T) {
	assert.False(t, isConfig("blabla"))
	assert.True(t, isConfig("blabla.json"))
	assert.True(t, isConfig("blabla.yaml"))
}

func TestRepository(t *testing.T) {
	const (
		repopath = "../../tests/fixtures/configs"
	)

	var (
		expectedPcfg      = []string{"img_status"}
		expectedAggcfg    = []string{"http_ok"}
		expectedLockHosts = []string{"ZK1:2181", "ZK2:2181"}
	)

	repo, err := NewFilesystemRepository(repopath)
	if !assert.Nil(t, err, "Unable to create repo %s", err) {
		t.Fatal()
	}

	lp, _ := repo.ListParsingConfigs()
	assert.Equal(t, expectedPcfg, lp, "")
	la, _ := repo.ListAggregationConfigs()
	assert.Equal(t, expectedAggcfg, la, "")

	cmbCg := repo.GetCombainerConfig()
	assert.Equal(t, expectedLockHosts, cmbCg.LockServerSection.Hosts, "")

	if len(cmbCg.CloudSection.HostFetcher) == 0 {
		t.Fatal("section isn't supposed to empty")
	}

	for _, name := range lp {
		pcfg, err := repo.GetParsingConfig(name)
		if !assert.Nil(t, err, "unable to read %s: %s", name, err) {
			t.Fatal()
		}

		if !assert.NotNil(t, pcfg, "ooops") {
			t.Fatal()
		}

		var decodedCfg ParsingConfig
		assert.Nil(t, pcfg.Decode(&decodedCfg), "unable to Decode parsing config")
	}

	for _, name := range la {
		pcfg, err := repo.GetAggregationConfig(name)
		if !assert.Nil(t, err, "unable to read %s: %s", name, err) {
			t.Fatal()
		}

		if !assert.NotNil(t, pcfg, "ooops") {
			t.Fatal()
		}

		var decodedCfg AggregationConfig
		assert.Nil(t, pcfg.Decode(&decodedCfg), "unable to Decode aggregation config")
	}
}
