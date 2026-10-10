package secrets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
)

// FlattenJSON turns a JSON document into dotted key/value pairs (db.password,
// hosts[0]) in document order, which a map would lose. Each field also keeps
// its path segments, so the tree view can rebuild the document's shape.
func FlattenJSON(data []byte) ([]Field, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var out []Field
	if err := walk(dec, "", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func walk(dec *json.Decoder, prefix string, path []PathSeg, out *[]Field) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	var val string
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return err
				}
				name, _ := kt.(string)
				k := name
				if prefix != "" {
					k = prefix + "." + k
				}
				if err := walk(dec, k, append(path, PathSeg{Key: name}), out); err != nil {
					return err
				}
			}
		case '[':
			for i := 0; dec.More(); i++ {
				if err := walk(dec, fmt.Sprintf("%s[%d]", prefix, i), append(path, PathSeg{Index: i, IsIndex: true}), out); err != nil {
					return err
				}
			}
		}
		_, err := dec.Token() // closing delimiter
		return err
	case string:
		val = t
	case json.Number:
		val = t.String()
	case bool:
		val = strconv.FormatBool(t)
	case nil:
		val = "null"
	}
	// Cloned: siblings append to the same backing array.
	*out = append(*out, Field{Key: prefix, Value: val, Path: slices.Clone(path)})
	return nil
}
