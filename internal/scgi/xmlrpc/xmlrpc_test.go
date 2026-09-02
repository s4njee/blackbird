package xmlrpc

import (
	"bytes"
	"encoding/xml"
	"errors"
	"strings"
	"testing"

)

func S(s string) Value  { return Value{Type: "string", Str: s} }
func I(n int64) Value   { return Value{Type: "int", Int: n} }
func B(b bool) Value    { return Value{Type: "bool", Bool: b} }

func TestEncodeRequestScalars(t *testing.T) {
	got := string(EncodeRequest("d.start", []Value{S("ABC123"), I(3), B(true)}))
	want := `<?xml version="1.0" encoding="UTF-8"?>
<methodCall><methodName>d.start</methodName><params>` +
		`<param><value><string>ABC123</string></value></param>` +
		`<param><value><i8>3</i8></value></param>` +
		`<param><value><boolean>1</boolean></value></param>` +
		`</params></methodCall>`
	if got != want {
		t.Fatalf("encode mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestEncodeRequestEscaping(t *testing.T) {
	got := string(EncodeRequest("x", []Value{S(`a<b>&c`)}))
	if !strings.Contains(got, "a&lt;b&gt;&amp;c") {
		t.Fatalf("string not escaped: %s", got)
	}
}

func TestEncodeRequestNested(t *testing.T) {
	arr := Value{Type: "array", Array: []Value{S("a"), I(1)}}
	st := Value{Type: "struct", Struct: []Member{
		{Name: "methodName", Value: S("d.multicall2")},
		{Name: "params", Value: arr},
	}}
	got := string(EncodeRequest("system.multicall", []Value{st}))
	want := `<?xml version="1.0" encoding="UTF-8"?>
<methodCall><methodName>system.multicall</methodName><params><param><value><struct>` +
		`<member><name>methodName</name><value><string>d.multicall2</string></value></member>` +
		`<member><name>params</name><value><array><data><value><string>a</string></value><value><i8>1</i8></value></data></array></value></member>` +
		`</struct></value></param></params></methodCall>`
	if got != want {
		t.Fatalf("encode mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestRequestRoundTrip(t *testing.T) {
	params := []Value{
		S(""),
		S("d.multicall2"),
		{Type: "array", Array: []Value{S("hash1"), S("hash2")}},
		{Type: "struct", Struct: []Member{{Name: "k", Value: I(42)}}},
		B(false),
	}
	method, got, err := DecodeRequest(EncodeRequest("load.start", params))
	if err != nil {
		t.Fatal(err)
	}
	if method != "load.start" {
		t.Fatalf("method = %q", method)
	}
	if len(got) != len(params) {
		t.Fatalf("param count = %d", len(got))
	}
	wantArr := params[2].Array
	gotArr := got[2].Array
	if len(gotArr) != len(wantArr) || gotArr[0].Str != "hash1" || gotArr[1].Str != "hash2" {
		t.Fatalf("array round-trip mismatch: %+v", gotArr)
	}
	m, _ := got[3].Member("k")
	if m.Int != 42 {
		t.Fatalf("struct round-trip mismatch: %+v", m)
	}
	if got[4].Bool {
		t.Fatal("bool round-trip mismatch")
	}
	if got[0].Str != "" {
		t.Fatalf("string round-trip mismatch: %+v", got[0])
	}
}

// Recorded-shape rtorrent responses.

const respScalars = `<?xml version="1.0" encoding="UTF-8"?>
<methodResponse><params><param><value>
  <array><data>
    <value><string>ubuntu-24.04.iso</string></value>
    <value><i8>3623878664</i8></value>
    <value><i8>0</i8></value>
    <value><boolean>1</boolean></value>
    <value><double>2.41</double></value>
  </data></array>
</value></param></params></methodResponse>`

func TestDecodeResponseScalars(t *testing.T) {
	vals, err := DecodeResponse([]byte(respScalars))
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 1 {
		t.Fatalf("param count = %d", len(vals))
	}
	arr := vals[0].Array
	if len(arr) != 5 {
		t.Fatalf("array len = %d", len(arr))
	}
	if arr[0].Str != "ubuntu-24.04.iso" || arr[1].Int != 3623878664 ||
		arr[3].Bool != true || arr[4].Dbl != 2.41 {
		t.Fatalf("scalar mismatch: %+v", arr)
	}
}

// Inline string values (no <string> wrapper) are legal XML-RPC.
const respInline = `<?xml version="1.0"?>
<methodResponse><params><param><value>0.15.4</value></param></params></methodResponse>`

func TestDecodeResponseInlineString(t *testing.T) {
	vals, err := DecodeResponse([]byte(respInline))
	if err != nil {
		t.Fatal(err)
	}
	if vals[0].Str != "0.15.4" {
		t.Fatalf("inline string = %q", vals[0].Str)
	}
}

func TestDecodeResponseEmptyValue(t *testing.T) {
	resp := `<?xml version="1.0"?><methodResponse><params><param><value></value></param></params></methodResponse>`
	vals, err := DecodeResponse([]byte(resp))
	if err != nil {
		t.Fatal(err)
	}
	if vals[0].Type != "string" || vals[0].Str != "" {
		t.Fatalf("empty value = %+v", vals[0])
	}
}

// Recorded fault shape from rtorrent (unknown command).
const respFault = `<?xml version="1.0" encoding="UTF-8"?>
<methodResponse><fault><value><struct>
  <member><name>faultCode</name><value><int>-501</int></value></member>
  <member><name>faultString</name><value><string>Could not find called command.</string></value></member>
</struct></value></fault></methodResponse>`

func TestDecodeFault(t *testing.T) {
	_, err := DecodeResponse([]byte(respFault))
	if err == nil {
		t.Fatal("expected fault")
	}
	var fault *Fault
	if !errors.As(err, &fault) {
		t.Fatalf("error type = %T, want *Fault", err)
	}
	if fault.Code != -501 || fault.String != "Could not find called command." {
		t.Fatalf("fault = %+v", fault)
	}
}

func TestDecodeMalformed(t *testing.T) {
	if _, err := DecodeResponse([]byte("not xml at all")); err == nil {
		t.Fatal("expected error for malformed response")
	}
	var v Value
	if err := xml.Unmarshal([]byte(`<value><unknown/></value>`), &v); err == nil {
		t.Fatal("expected error for unknown value type")
	}
}

func TestBase64RoundTrip(t *testing.T) {
	payload := []byte{0x00, 0x01, 0xff, 0xfe, 'd'}
	req := EncodeRequest("load.raw_start", []Value{S(""), Base64Value(payload)})
	method, params, err := DecodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if method != "load.raw_start" || len(params) != 2 {
		t.Fatalf("request = %s %d params", method, len(params))
	}
	if params[1].Type != "base64" || !bytes.Equal([]byte(params[1].Str), payload) {
		t.Fatalf("base64 round-trip mismatch: %+v", params[1])
	}
}
