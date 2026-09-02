package xmlrpc

import (
	"fmt"
	"testing"
)

func TestDebugNested(t *testing.T) {
	rows := Value{Type: "array", Array: []Value{
		{Type: "array", Array: []Value{{Type: "string", Str: "h1"}, {Type: "int", Int: 1}}},
		{Type: "array", Array: []Value{{Type: "string", Str: "h2"}, {Type: "int", Int: 2}}},
	}}
	resp := EncodeResponse(rows)
	fmt.Println(string(resp))
	vals, err := DecodeResponse(resp)
	fmt.Println("err:", err, "vals:", len(vals))
	if len(vals) > 0 {
		fmt.Printf("top type=%s arr=%d\n", vals[0].Type, len(vals[0].Array))
		for i, row := range vals[0].Array {
			fmt.Printf("row %d: type=%s n=%d\n", i, row.Type, len(row.Array))
		}
	}
}
