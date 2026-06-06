package secure_test

import (
	"testing"

	"github.com/dobbo-ca/lynceus/internal/secure"
)

func TestCheckDatabaseDSN(t *testing.T) {
	cases := []struct {
		name      string
		dsn       string
		require   bool
		wantError bool
	}{
		{"url require ok", "postgres://u:p@host:5432/db?sslmode=require", true, false},
		{"url verify-full ok", "postgres://u:p@host/db?sslmode=verify-full", true, false},
		{"url disable rejected", "postgres://u:p@host/db?sslmode=disable", true, true},
		{"url prefer rejected", "postgres://u:p@host/db?sslmode=prefer", true, true},
		{"url unset rejected (fail closed)", "postgres://u:p@host/db", true, true},
		{"keyword require ok", "host=h user=u dbname=d sslmode=require", true, false},
		{"keyword disable rejected", "host=h user=u dbname=d sslmode=disable", true, true},
		{"keyword unset rejected", "host=h user=u dbname=d", true, true},
		{"enforcement off allows disable", "postgres://u@host/db?sslmode=disable", false, false},
		{"case-insensitive mode", "postgres://u@host/db?sslmode=REQUIRE", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := secure.CheckDatabaseDSN(c.dsn, c.require)
			if (err != nil) != c.wantError {
				t.Fatalf("CheckDatabaseDSN(%q, %v) error = %v, wantError = %v",
					c.dsn, c.require, err, c.wantError)
			}
		})
	}
}

func TestCheckWebsocketURL(t *testing.T) {
	cases := []struct {
		name      string
		url       string
		require   bool
		wantError bool
	}{
		{"wss ok", "wss://ingest.example.com/v1/ingest", true, false},
		{"ws rejected", "ws://ingest.example.com/v1/ingest", true, true},
		{"http rejected", "http://ingest.example.com", true, true},
		{"enforcement off allows ws", "ws://localhost:8081/v1/ingest", false, false},
		{"case-insensitive scheme", "WSS://ingest.example.com", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := secure.CheckWebsocketURL(c.url, c.require)
			if (err != nil) != c.wantError {
				t.Fatalf("CheckWebsocketURL(%q, %v) error = %v, wantError = %v",
					c.url, c.require, err, c.wantError)
			}
		})
	}
}
