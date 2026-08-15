package middleware

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestTypedCursorValuesRoundTrip(t *testing.T) {
	t.Parallel()

	stamp := time.Date(2026, 8, 13, 15, 4, 5, 0, time.UTC)
	original := typedCursorValues{
		nil,
		true,
		"id-1",
		1,
		int8(2),
		int16(3),
		int32(4),
		int64(5),
		uint(6),
		uint8(7),
		uint16(8),
		uint32(9),
		uint64(10),
		float32(1.5),
		float64(2.5),
		json.Number("3.10"),
		stamp,
		[]byte("ab"),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal typed cursor values: %v", err)
	}

	var restored typedCursorValues
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal typed cursor values: %v", err)
	}
	if len(restored) != len(original) {
		t.Fatalf("expected %d values, got %d", len(original), len(restored))
	}
	for i := range original {
		if !cursorValueEqual(original[i], restored[i]) {
			t.Fatalf("index %d: original %T(%[2]v) restored %T(%[3]v)", i, original[i], restored[i])
		}
	}
}

func TestTypedCursorValuesLegacyIntegerBecomesInt64(t *testing.T) {
	t.Parallel()

	var restored typedCursorValues
	if err := json.Unmarshal([]byte(`[12, 3.5, "x"]`), &restored); err != nil {
		t.Fatalf("unmarshal legacy cursor values: %v", err)
	}
	if len(restored) != 3 {
		t.Fatalf("expected 3 values, got %#v", restored)
	}
	if _, ok := restored[0].(int64); !ok || restored[0].(int64) != 12 {
		t.Fatalf("expected int64(12), got %T %[1]v", restored[0])
	}
	if _, ok := restored[1].(float64); !ok || restored[1].(float64) != 3.5 {
		t.Fatalf("expected float64(3.5), got %T %[1]v", restored[1])
	}
	if restored[2] != "x" {
		t.Fatalf("expected string x, got %#v", restored[2])
	}
}

func cursorValueEqual(left any, right any) bool {
	leftTime, leftIsTime := left.(time.Time)
	rightTime, rightIsTime := right.(time.Time)
	if leftIsTime || rightIsTime {
		return leftIsTime && rightIsTime && leftTime.Equal(rightTime)
	}
	return reflect.DeepEqual(left, right)
}
