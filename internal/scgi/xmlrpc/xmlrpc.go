// Package xmlrpc implements the XML-RPC request/response codec used over the
// SCGI transport to talk to rtorrent. Values decode into the generic Value
// tree; typed extraction happens in the rtorrent client layer.
package xmlrpc

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// Value is a generic XML-RPC value. Type is one of
// "string", "int", "bool", "double", "array", "struct", "base64",
// "datetime", "nil". rtorrent mostly emits i8 (ints), strings, arrays and
// structs; the codec also accepts the rest of the spec for robustness.
type Value struct {
	Type   string
	Str    string // string, base64 (decoded), datetime
	Int    int64  // int / i8
	Bool   bool
	Dbl    float64
	Array  []Value
	Struct []Member
}

// Member is one name/value pair of a struct value.
type Member struct {
	Name  string
	Value Value
}

// Fault is a typed XML-RPC fault. Transport errors never surface as Fault,
// so callers can distinguish daemon rejections from connectivity problems.
type Fault struct {
	Code   int
	String string
}

func (f *Fault) Error() string {
	return fmt.Sprintf("rtorrent fault %d: %s", f.Code, f.String)
}

// Member returns the named struct member and whether it was present.
func (v Value) Member(name string) (Value, bool) {
	for _, m := range v.Struct {
		if m.Name == name {
			return m.Value, true
		}
	}
	return Value{}, false
}

// ---- Encoding ----

var escaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// EncodeRequest builds a full XML-RPC methodCall document.
func EncodeRequest(method string, params []Value) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header[:len(xml.Header)-1]) // "<?xml version=..." without trailing newline handled below
	b.WriteString("\n")
	b.WriteString("<methodCall><methodName>")
	escaper.WriteString(&b, method)
	b.WriteString("</methodName><params>")
	for _, p := range params {
		b.WriteString("<param>")
		AppendValue(&b, p)
		b.WriteString("</param>")
	}
	b.WriteString("</params></methodCall>")
	return b.Bytes()
}

// AppendValue writes one <value> element, including binary payloads
// (base64) used by load.raw* calls.
func AppendValue(b *bytes.Buffer, v Value) {
	b.WriteString("<value>")
	switch v.Type {
	case "int", "i8":
		fmt.Fprintf(b, "<i8>%d</i8>", v.Int)
	case "bool":
		if v.Bool {
			b.WriteString("<boolean>1</boolean>")
		} else {
			b.WriteString("<boolean>0</boolean>")
		}
	case "double":
		fmt.Fprintf(b, "<double>%s</double>", strconv.FormatFloat(v.Dbl, 'f', -1, 64))
	case "array":
		b.WriteString("<array><data>")
		for _, e := range v.Array {
			AppendValue(b, e)
		}
		b.WriteString("</data></array>")
	case "struct":
		b.WriteString("<struct>")
		for _, m := range v.Struct {
			b.WriteString("<member><name>")
			escaper.WriteString(b, m.Name)
			b.WriteString("</name>")
			AppendValue(b, m.Value)
			b.WriteString("</member>")
		}
		b.WriteString("</struct>")
	case "base64":
		b.WriteString("<base64>")
		b.WriteString(v.Str) // pre-encoded base64 payload
		b.WriteString("</base64>")
	case "datetime":
		b.WriteString("<dateTime.iso8601>")
		escaper.WriteString(b, v.Str)
		b.WriteString("</dateTime.iso8601>")
	case "nil":
		b.WriteString("<nil/>")
	default: // "string" and "" (empty value defaults to string)
		b.WriteString("<string>")
		escaper.WriteString(b, v.Str)
		b.WriteString("</string>")
	}
	b.WriteString("</value>")
}

// Base64Value builds a base64-typed value from raw bytes (XML-RPC has no
// true binary type; rtorrent accepts base64 for load.raw*).
func Base64Value(data []byte) Value {
	return Value{Type: "base64", Str: base64.StdEncoding.EncodeToString(data)}
}

// EncodeResponse builds a methodResponse wrapping a single param value.
func EncodeResponse(v Value) []byte {
	return EncodeResponseParams([]Value{v})
}

// EncodeResponseParams builds a methodResponse with one <param> per value.
func EncodeResponseParams(vals []Value) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString("<methodResponse><params>")
	for _, v := range vals {
		b.WriteString("<param>")
		AppendValue(&b, v)
		b.WriteString("</param>")
	}
	b.WriteString("</params></methodResponse>")
	return b.Bytes()
}

// EncodeFault builds a fault methodResponse.
func EncodeFault(code int, message string) []byte {
	fault := Value{Type: "struct", Struct: []Member{
		{Name: "faultCode", Value: Value{Type: "int", Int: int64(code)}},
		{Name: "faultString", Value: Value{Type: "string", Str: message}},
	}}
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString("<methodResponse><fault>")
	AppendValue(&b, fault)
	b.WriteString("</fault></methodResponse>")
	return b.Bytes()
}

// ---- Decoding ----

// DecodeResponse parses a methodResponse, returning the response params.
// A fault block is returned as *Fault.
func DecodeResponse(data []byte) ([]Value, error) {
	var env struct {
		XMLName xml.Name `xml:"methodResponse"`
		Params  struct {
			Param []struct {
				Value Value `xml:"value"`
			} `xml:"param"`
		} `xml:"params"`
		Fault *struct {
			Value Value `xml:"value"`
		} `xml:"fault"`
	}
	if err := xml.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("xml-rpc: decode response: %w", err)
	}
	if env.Fault != nil {
		f := &Fault{}
		if code, ok := env.Fault.Value.Member("faultCode"); ok {
			f.Code = int(code.Int)
		}
		if str, ok := env.Fault.Value.Member("faultString"); ok {
			f.String = str.Str
		}
		return nil, f
	}
	out := make([]Value, 0, len(env.Params.Param))
	for _, p := range env.Params.Param {
		out = append(out, p.Value)
	}
	return out, nil
}

// DecodeRequest parses a methodCall document. Used by the test fake servers
// and any future proxy surface.
func DecodeRequest(data []byte) (method string, params []Value, err error) {
	var req struct {
		XMLName    xml.Name `xml:"methodCall"`
		MethodName string   `xml:"methodName"`
		Params     struct {
			Param []struct {
				Value Value `xml:"value"`
			} `xml:"param"`
		} `xml:"params"`
	}
	if err := xml.Unmarshal(data, &req); err != nil {
		return "", nil, fmt.Errorf("xml-rpc: decode request: %w", err)
	}
	for _, p := range req.Params.Param {
		params = append(params, p.Value)
	}
	return req.MethodName, params, nil
}

// UnmarshalXML implements xml.Unmarshaler for <value>, accepting both the
// inline form (<value>text</value>) and typed children.
func (v *Value) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	return v.unmarshalDepth(d, start, 0)
}

// maxValueDepth bounds <value> nesting. The parser below walks the token
// stream itself rather than going through Decoder.unmarshal, so
// encoding/xml's own recursion guard never engages: without this limit a
// response of nothing but repeated "<value><array><data>" open tags
// recurses until the goroutine stack overflows, which is a runtime throw
// that recover() cannot catch and so takes the whole process down. The
// response byte cap does not help — the crash fits well inside it.
const maxValueDepth = 100

func (v *Value) unmarshalDepth(d *xml.Decoder, start xml.StartElement, depth int) error {
	if depth > maxValueDepth {
		return fmt.Errorf("xml-rpc: value nesting deeper than %d levels", maxValueDepth)
	}
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.CharData:
			if strings.TrimSpace(string(t)) == "" {
				continue
			}
			*v = Value{Type: "string", Str: string(t)}
		case xml.StartElement:
			val, err := parseTyped(d, t, depth)
			if err != nil {
				return err
			}
			*v = val
		case xml.EndElement:
			// <value/> or the closing tag of an inline string
			if v.Type == "" {
				*v = Value{Type: "string"}
			}
			return nil
		}
	}
}

// parseTyped decodes a typed value element whose StartElement has already
// been read; it consumes through the element's EndElement.
func parseTyped(d *xml.Decoder, se xml.StartElement, depth int) (Value, error) {
	switch se.Name.Local {
	case "string":
		s, err := readChars(d)
		return Value{Type: "string", Str: s}, err
	case "i4", "int", "i8":
		s, err := readChars(d)
		if err != nil {
			return Value{}, err
		}
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return Value{}, fmt.Errorf("xml-rpc: bad integer %q: %w", s, err)
		}
		return Value{Type: "int", Int: n}, nil
	case "boolean":
		s, err := readChars(d)
		if err != nil {
			return Value{}, err
		}
		return Value{Type: "bool", Bool: strings.TrimSpace(s) == "1"}, nil
	case "double":
		s, err := readChars(d)
		if err != nil {
			return Value{}, err
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return Value{}, fmt.Errorf("xml-rpc: bad double %q: %w", s, err)
		}
		return Value{Type: "double", Dbl: f}, nil
	case "base64":
		s, err := readChars(d)
		if err != nil {
			return Value{}, err
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		if err != nil {
			return Value{}, fmt.Errorf("xml-rpc: bad base64: %w", err)
		}
		return Value{Type: "base64", Str: string(raw)}, nil
	case "dateTime.iso8601":
		s, err := readChars(d)
		return Value{Type: "datetime", Str: s}, err
	case "nil":
		if err := skipElement(d); err != nil {
			return Value{}, err
		}
		return Value{Type: "nil"}, nil
	case "array":
		return parseArray(d, depth)
	case "struct":
		return parseStruct(d, depth)
	default:
		return Value{}, fmt.Errorf("xml-rpc: unsupported value type <%s>", se.Name.Local)
	}
}

func parseArray(d *xml.Decoder, depth int) (Value, error) {
	out := Value{Type: "array"}
	inData := false
	for {
		tok, err := d.Token()
		if err != nil {
			return Value{}, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "data":
				inData = true
			case "value":
				if !inData {
					return Value{}, fmt.Errorf("xml-rpc: <value> outside <data> in array")
				}
				var v Value
				if err := v.unmarshalDepth(d, t, depth+1); err != nil {
					return Value{}, err
				}
				out.Array = append(out.Array, v)
			default:
				return Value{}, fmt.Errorf("xml-rpc: unexpected <%s> in array", t.Name.Local)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "data":
				inData = false
			case "array":
				return out, nil
			default:
				return Value{}, fmt.Errorf("xml-rpc: unexpected </%s> in array", t.Name.Local)
			}
		}
	}
}

func parseStruct(d *xml.Decoder, depth int) (Value, error) {
	out := Value{Type: "struct"}
	var cur *Member
	for {
		tok, err := d.Token()
		if err != nil {
			return Value{}, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "member":
				out.Struct = append(out.Struct, Member{})
				cur = &out.Struct[len(out.Struct)-1]
			case "name":
				if cur == nil {
					return Value{}, fmt.Errorf("xml-rpc: <name> outside <member>")
				}
				cur.Name, err = readChars(d)
				if err != nil {
					return Value{}, err
				}
			case "value":
				if cur == nil {
					return Value{}, fmt.Errorf("xml-rpc: <value> outside <member>")
				}
				if err := cur.Value.unmarshalDepth(d, t, depth+1); err != nil {
					return Value{}, err
				}
			default:
				return Value{}, fmt.Errorf("xml-rpc: unexpected <%s> in struct", t.Name.Local)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "member":
				cur = nil
			case "struct":
				return out, nil
			}
		}
	}
}

// readChars accumulates character data until the next EndElement.
func readChars(d *xml.Decoder) (string, error) {
	var b strings.Builder
	for {
		tok, err := d.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.StartElement:
			return "", fmt.Errorf("xml-rpc: unexpected <%s> inside scalar", t.Name.Local)
		case xml.EndElement:
			return b.String(), nil
		}
	}
}

// skipElement consumes a full element (start tag already read).
func skipElement(d *xml.Decoder) error {
	depth := 1
	for depth > 0 {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return nil
}
