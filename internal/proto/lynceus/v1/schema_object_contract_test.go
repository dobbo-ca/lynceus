// Package lynceusv1_test enforces the T1 privacy contract for the
// schema-object inventory message. SchemaObject is allowed to carry
// schema/relation NAMES (those are the inventory's value; privacy for
// names is enforced at the collector boundary via the ignore/include
// schema regex filter), but it MUST NOT carry any field capable of
// holding table DATA — no column samples, no row contents, no DEFAULT
// expressions, no comments, no ACLs, no constraint values.
package lynceusv1_test

import (
	"testing"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// TestSchemaObjectHasOnlyStructuralFields enforces the T1 invariant for
// SchemaObject: every field must be a structural identifier (schema /
// relation / kind / parent fqn), a size metric, a boolean catalog flag,
// or a first-seen timestamp. Any field that could carry table DATA
// (column samples, defaults, constraint values, free-form comments,
// ACL strings) must fail this test.
func TestSchemaObjectHasOnlyStructuralFields(t *testing.T) {
	allowed := map[string]struct{}{
		"kind":               {}, // ObjectKind enum
		"schema":             {}, // namespace name (sensitive — filtered upstream)
		"name":               {}, // relation/function/sequence name
		"fqn":                {}, // "schema.name" — derived, stable identifier
		"size_bytes":         {}, // pg_total_relation_size / pg_relation_size
		"is_partition":       {}, // pg_class.relispartition
		"parent_fqn":         {}, // inherited parent (partition parent), "" if none
		"first_seen_at_unix": {}, // stable timestamp from the stats DB
	}

	fields := (&lynceusv1.SchemaObject{}).ProtoReflect().Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		name := string(fields.Get(i).Name())
		if _, ok := allowed[name]; !ok {
			t.Fatalf(
				"unexpected field %q in T1 SchemaObject — possible DATA leak. "+
					"T1 SchemaObject may carry STRUCTURAL metadata only. If you "+
					"need column defaults, constraint values, comments, or ACLs, "+
					"define a separate T2 message gated behind RBAC + audit.",
				name,
			)
		}
	}
}

// TestSchemaObjectFieldKinds guards against silent type widenings that
// could let an attacker stuff arbitrary bytes through a "scalar" field.
func TestSchemaObjectFieldKinds(t *testing.T) {
	fields := (&lynceusv1.SchemaObject{}).ProtoReflect().Descriptor().Fields()

	wantKind := map[string]string{
		"schema":             "string",
		"name":               "string",
		"fqn":                "string",
		"parent_fqn":         "string",
		"size_bytes":         "int64",
		"is_partition":       "bool",
		"first_seen_at_unix": "int64",
	}
	for n, want := range wantKind {
		f := fields.ByName(protoName(n))
		if f == nil {
			t.Fatalf("SchemaObject missing required field %q", n)
		}
		if got := f.Kind().String(); got != want {
			t.Fatalf("SchemaObject.%s kind = %q, want %q", n, got, want)
		}
	}

	// kind must be the ObjectKind enum, not a free-form string.
	kindField := fields.ByName("kind")
	if kindField == nil {
		t.Fatal("SchemaObject missing required field \"kind\"")
	}
	if kindField.Kind().String() != "enum" {
		t.Fatalf("SchemaObject.kind kind = %q, want \"enum\" (ObjectKind)", kindField.Kind().String())
	}
}

// TestSnapshotCarriesSchemaObjects enforces that the existing Snapshot
// message carries a repeated SchemaObject — the inventory ships on the
// existing wire envelope, no second protocol.
func TestSnapshotCarriesSchemaObjects(t *testing.T) {
	fields := (&lynceusv1.Snapshot{}).ProtoReflect().Descriptor().Fields()
	f := fields.ByName("schema_objects")
	if f == nil {
		t.Fatal("Snapshot missing required repeated field \"schema_objects\"")
	}
	if !f.IsList() {
		t.Fatal("Snapshot.schema_objects must be repeated")
	}
	if f.Message() == nil || string(f.Message().Name()) != "SchemaObject" {
		t.Fatalf("Snapshot.schema_objects must be repeated SchemaObject, got %v", f.Message())
	}
}

func protoName(n string) protoreflect.Name { return protoreflect.Name(n) }
