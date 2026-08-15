package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// typedCursorValues 在缓存中保存游标值，并保留原始数值类型
// 标准 encoding/json 会把 []any 中的数字解成 float64，续查时类型不稳定
type typedCursorValues []any

// encodedCursorValue 是单个游标值的类型标签编码
// Type 记录原始 Go 类型名，Value 是该类型的 JSON 载荷，读回时按 Type 还原
type encodedCursorValue struct {
	Type  string          `json:"t"`
	Value json.RawMessage `json:"v,omitempty"`
}

// MarshalJSON 将游标切片编码为带类型标签的 JSON 数组
// nil 写成 JSON null，以区分“未设置游标”和“空游标列表”
func (v typedCursorValues) MarshalJSON() ([]byte, error) {
	if v == nil {
		return []byte("null"), nil
	}
	encoded := make([]encodedCursorValue, len(v))
	for i, item := range v {
		encoded[i] = encodeCursorValue(item)
	}
	return json.Marshal(encoded)
}

// UnmarshalJSON 从缓存字节还原游标切片
// 优先按带类型标签的新格式解码；旧缓存是无标签 JSON 数组，走 UseNumber 后再把整数收成 int64，避免变成 float64
func (v *typedCursorValues) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		*v = nil
		return nil
	}
	if looksTaggedCursorArray(data) {
		var tagged []encodedCursorValue
		if err := json.Unmarshal(data, &tagged); err != nil {
			return err
		}
		out := make([]any, 0, len(tagged))
		for _, item := range tagged {
			decoded, err := decodeCursorValue(item)
			if err != nil {
				return err
			}
			out = append(out, decoded)
		}
		*v = out
		return nil
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw []any
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	*v = coerceLegacyCursorValues(raw)
	return nil
}

// looksTaggedCursorArray 判断 data 是否为新版带类型标签的游标数组
// 空数组按新格式处理；首元素缺少 t 字段或整体不是对象数组时视为旧格式
func looksTaggedCursorArray(data []byte) bool {
	var probe []map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	if len(probe) == 0 {
		return true
	}
	_, ok := probe[0]["t"]
	return ok
}

// encodeCursorValue 把单个游标值编成类型标签
// 常见标量、time.Time、[]byte 走具名类型；其余可序列化值降级为 json，失败再降级为类型名加字符串
func encodeCursorValue(value any) encodedCursorValue {
	if value == nil {
		return encodedCursorValue{Type: "nil"}
	}
	switch typed := value.(type) {
	case bool:
		return mustEncodeCursorValue("bool", typed)
	case string:
		return mustEncodeCursorValue("string", typed)
	case int:
		return mustEncodeCursorValue("int", typed)
	case int8:
		return mustEncodeCursorValue("int8", typed)
	case int16:
		return mustEncodeCursorValue("int16", typed)
	case int32:
		return mustEncodeCursorValue("int32", typed)
	case int64:
		return mustEncodeCursorValue("int64", typed)
	case uint:
		return mustEncodeCursorValue("uint", typed)
	case uint8:
		return mustEncodeCursorValue("uint8", typed)
	case uint16:
		return mustEncodeCursorValue("uint16", typed)
	case uint32:
		return mustEncodeCursorValue("uint32", typed)
	case uint64:
		return mustEncodeCursorValue("uint64", typed)
	case float32:
		return mustEncodeCursorValue("float32", typed)
	case float64:
		return mustEncodeCursorValue("float64", typed)
	case json.Number:
		return mustEncodeCursorValue("number", typed.String())
	case time.Time:
		return mustEncodeCursorValue("time", typed.UTC().Format(time.RFC3339Nano))
	case []byte:
		return mustEncodeCursorValue("bytes", typed)
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return mustEncodeCursorValue("string", fmt.Sprintf("%T:%v", typed, typed))
		}
		return encodedCursorValue{Type: "json", Value: raw}
	}
}

// mustEncodeCursorValue 按 typeName 写出游标值的 JSON 载荷
// json.Marshal 失败时退回纯字符串，保证缓存写入不会因单个无法编码的值中断
func mustEncodeCursorValue(typeName string, value any) encodedCursorValue {
	raw, err := json.Marshal(value)
	if err != nil {
		raw, _ = json.Marshal(fmt.Sprint(value))
		return encodedCursorValue{Type: "string", Value: raw}
	}
	return encodedCursorValue{Type: typeName, Value: raw}
}

// decodeCursorValue 按类型标签把单个游标值还原为原始 Go 类型
// 未知 Type 返回错误，避免静默解成错误类型后继续分页
func decodeCursorValue(encoded encodedCursorValue) (any, error) {
	switch encoded.Type {
	case "", "nil":
		return nil, nil
	case "bool":
		var value bool
		return value, json.Unmarshal(encoded.Value, &value)
	case "string":
		var value string
		return value, json.Unmarshal(encoded.Value, &value)
	case "int":
		var value int
		return value, json.Unmarshal(encoded.Value, &value)
	case "int8":
		var value int8
		return value, json.Unmarshal(encoded.Value, &value)
	case "int16":
		var value int16
		return value, json.Unmarshal(encoded.Value, &value)
	case "int32":
		var value int32
		return value, json.Unmarshal(encoded.Value, &value)
	case "int64":
		var value int64
		return value, json.Unmarshal(encoded.Value, &value)
	case "uint":
		var value uint
		return value, json.Unmarshal(encoded.Value, &value)
	case "uint8":
		var value uint8
		return value, json.Unmarshal(encoded.Value, &value)
	case "uint16":
		var value uint16
		return value, json.Unmarshal(encoded.Value, &value)
	case "uint32":
		var value uint32
		return value, json.Unmarshal(encoded.Value, &value)
	case "uint64":
		var value uint64
		return value, json.Unmarshal(encoded.Value, &value)
	case "float32":
		var value float32
		return value, json.Unmarshal(encoded.Value, &value)
	case "float64":
		var value float64
		return value, json.Unmarshal(encoded.Value, &value)
	case "number":
		var value string
		if err := json.Unmarshal(encoded.Value, &value); err != nil {
			return nil, err
		}
		return json.Number(value), nil
	case "time":
		var value string
		if err := json.Unmarshal(encoded.Value, &value); err != nil {
			return nil, err
		}
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return nil, err
		}
		return parsed, nil
	case "bytes":
		var value []byte
		return value, json.Unmarshal(encoded.Value, &value)
	case "json":
		var value any
		decoder := json.NewDecoder(bytes.NewReader(encoded.Value))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return coerceLegacyCursorValue(value), nil
	default:
		return nil, fmt.Errorf("unsupported cursor value type %q", encoded.Type)
	}
}

// coerceLegacyCursorValues 还原旧版无标签游标数组中的每个元素
func coerceLegacyCursorValues(values []any) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = coerceLegacyCursorValue(value)
	}
	return out
}

// coerceLegacyCursorValue 把旧缓存里的 json.Number 收成整数或浮点
// 能整除的数字还原为 int64，避免 encoding/json 默认的 float64 破坏后续 SetCursorValue
func coerceLegacyCursorValue(value any) any {
	switch typed := value.(type) {
	case json.Number:
		if i, err := typed.Int64(); err == nil {
			return i
		}
		if f, err := typed.Float64(); err == nil {
			return f
		}
		return typed.String()
	case []any:
		return coerceLegacyCursorValues(typed)
	case map[string]any:
		normalized := make(map[string]any, len(typed))
		for key, nested := range typed {
			normalized[key] = coerceLegacyCursorValue(nested)
		}
		return normalized
	default:
		return value
	}
}
