package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// hashDomain is the v1 domain separator. Bump this (and the migration
// note in 0002_audit_chain.sql) if the canonical layout below changes.
const hashDomain = "lynceus.audit.v1\x00"

// genesisPrev is the 32-byte zero value used as prev_hash for row id 1.
var genesisPrev = make([]byte, 32)

// hashAuditRow returns SHA-256 over the canonical byte layout specified
// in the plan doc. detail is the canonical JSON bytes of the row's JSONB
// column (zero length when SQL NULL). at is truncated to nanosecond UTC.
func hashAuditRow(id uint64, prev []byte, actor, action, serverID string, tier int16, detail []byte, at time.Time) []byte {
	if len(prev) != 32 {
		panic(fmt.Sprintf("hashAuditRow: prev must be 32 bytes, got %d", len(prev)))
	}
	var buf bytes.Buffer
	buf.WriteString(hashDomain)

	var u64 [8]byte
	binary.BigEndian.PutUint64(u64[:], id)
	buf.Write(u64[:])

	buf.Write(prev)

	writeLP := func(s string) {
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], uint32(len(s)))
		buf.Write(l[:])
		buf.WriteString(s)
	}
	writeLP(actor)
	writeLP(action)
	writeLP(serverID)

	var i16 [2]byte
	binary.BigEndian.PutUint16(i16[:], uint16(tier))
	buf.Write(i16[:])

	var dl [4]byte
	binary.BigEndian.PutUint32(dl[:], uint32(len(detail)))
	buf.Write(dl[:])
	buf.Write(detail)

	var ns [8]byte
	binary.BigEndian.PutUint64(ns[:], uint64(at.UTC().UnixNano()))
	buf.Write(ns[:])

	sum := sha256.Sum256(buf.Bytes())
	return sum[:]
}

// canonicalJSON re-serializes raw JSON bytes with object keys sorted
// lexicographically and no insignificant whitespace. Returns nil for
// a nil input; returns ("null", nil) for the literal JSON "null".
func canonicalJSON(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("canonicalJSON: parse: %w", err)
	}
	var buf bytes.Buffer
	if err := canonicalEmit(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func canonicalEmit(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		buf.WriteString(x.String())
	case string:
		b, err := json.Marshal(x) // RFC 8259 escaping
		if err != nil {
			return err
		}
		buf.Write(b)
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := canonicalEmit(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			if err := canonicalEmit(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("canonicalJSON: unsupported type %T", v)
	}
	return nil
}
