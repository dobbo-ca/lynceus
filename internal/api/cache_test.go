package api

import "testing"

func TestConfig_CacheEnabled(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"none", Config{}, false},
		{"redis", Config{EnableRedis: true}, true},
		{"valkey", Config{EnableValkey: true}, true},
		{"both", Config{EnableRedis: true, EnableValkey: true}, true},
	}
	for _, c := range cases {
		if got := c.cfg.CacheEnabled(); got != c.want {
			t.Errorf("%s: CacheEnabled()=%v want %v", c.name, got, c.want)
		}
	}
}
