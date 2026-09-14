// Package remote formats protobuf responses using descriptor-validated Go views.
package remote

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	clioutput "github.com/sir-hassan/inframill/internal/cli/output"
)

type Format int

const (
	Text Format = iota
	Enum
	Timestamp
	Commas
	Lines
)

type (
	Field struct {
		Key, Path, Header string
		Format            Format
	}
	Section    struct{ Path, Header string }
	Definition struct {
		Rows, Empty, NextCursor, ObservedAt, Failure string
		Fields                                       []Field
		Sections                                     []Section
	}
)

func field(message protoreflect.MessageDescriptor, path string) (protoreflect.FieldDescriptor, error) {
	var result protoreflect.FieldDescriptor
	for i, part := range strings.Split(path, ".") {
		if i > 0 {
			if result.Message() == nil || result.IsList() || result.IsMap() {
				return nil, errors.New("presentation path crosses a non-message")
			}
			message = result.Message()
		}
		result = message.Fields().ByName(protoreflect.Name(part))
		if result == nil {
			return nil, errors.New("unknown presentation field: " + path)
		}
	}
	return result, nil
}

func (d Definition) Validate(message protoreflect.MessageDescriptor) error {
	row := message
	if d.Rows != "" {
		f, err := field(message, d.Rows)
		if err != nil {
			return err
		}
		if !f.IsList() || f.Message() == nil {
			return errors.New("presentation rows require repeated messages")
		}
		row = f.Message()
	}
	keys := map[string]bool{}
	for _, column := range d.Fields {
		if column.Key == "" || keys[column.Key] {
			return errors.New("invalid presentation column key")
		}
		keys[column.Key] = true
		f, err := field(row, column.Path)
		if err != nil {
			return err
		}
		switch column.Format {
		case Text:
			if f.Message() != nil || f.IsList() || f.IsMap() {
				return errors.New("text requires a scalar")
			}
		case Enum:
			if f.Kind() != protoreflect.EnumKind || f.IsList() {
				return errors.New("enum requires a scalar enum")
			}
		case Timestamp:
			if f.Message() == nil || f.Message().FullName() != "google.protobuf.Timestamp" || f.IsList() {
				return errors.New("timestamp requires Timestamp")
			}
		case Commas, Lines:
			if !f.IsList() || f.Kind() != protoreflect.StringKind {
				return errors.New("joined field requires repeated strings")
			}
		default:
			return errors.New("unsupported presentation format")
		}
	}
	for _, section := range d.Sections {
		f, err := field(row, section.Path)
		if err != nil {
			return err
		}
		if f.Message() == nil || f.IsList() || f.IsMap() {
			return errors.New("JSON section requires a message")
		}
	}
	for _, path := range []string{d.NextCursor, d.ObservedAt, d.Failure} {
		if path == "" {
			continue
		}
		f, err := field(message, path)
		if err != nil {
			return err
		}
		switch path {
		case d.NextCursor:
			if f.Kind() != protoreflect.StringKind || f.IsList() {
				return errors.New("cursor requires a string")
			}
		case d.ObservedAt:
			if f.Message() == nil || f.Message().FullName() != "google.protobuf.Timestamp" {
				return errors.New("observed time requires Timestamp")
			}
		case d.Failure:
			if f.Message() == nil {
				return errors.New("failure requires a message")
			}
			for _, key := range []string{"code", "message", "path"} {
				child := f.Message().Fields().ByName(protoreflect.Name(key))
				if child == nil || child.Kind() != protoreflect.StringKind {
					return errors.New("invalid failure presentation")
				}
			}
		}
	}
	return nil
}

func Validate(message proto.Message, columns string, selected bool) error {
	d, ok := definitions[message.ProtoReflect().Descriptor().FullName()]
	if ok {
		if err := d.Validate(message.ProtoReflect().Descriptor()); err != nil {
			return err
		}
	}
	if !selected {
		return nil
	}
	if !ok || d.Rows == "" {
		return errors.New("columns require a configured list view")
	}
	_, err := project(d.Fields, columns)
	return err
}

func project(fields []Field, columns string) ([]Field, error) {
	var result []Field
	seen := map[string]bool{}
	for key := range strings.SplitSeq(columns, ",") {
		if seen[key] || key == "" {
			return nil, errors.New("columns must be nonempty and unique")
		}
		seen[key] = true
		found := false
		for _, f := range fields {
			if f.Key == key {
				result = append(result, f)
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("unknown column")
		}
	}
	return result, nil
}

func value(message protoreflect.Message, path string) (protoreflect.Value, protoreflect.FieldDescriptor) {
	var result protoreflect.Value
	var f protoreflect.FieldDescriptor
	for part := range strings.SplitSeq(path, ".") {
		f = message.Descriptor().Fields().ByName(protoreflect.Name(part))
		result = message.Get(f)
		if f.Message() != nil && !f.IsList() && !f.IsMap() {
			message = result.Message()
		}
	}
	return result, f
}

func cell(message protoreflect.Message, column Field) string {
	v, f := value(message, column.Path)
	switch column.Format {
	case Enum:
		item := f.Enum().Values().ByNumber(v.Enum())
		if item == nil {
			return fmt.Sprint(v.Enum())
		}
		prefix := string(f.Enum().Values().Get(0).Name())
		prefix = strings.TrimSuffix(prefix, "UNSPECIFIED")
		return strings.ToLower(strings.TrimPrefix(string(item.Name()), prefix))
	case Timestamp:
		if !v.Message().IsValid() {
			return ""
		}
		stamp, ok := v.Message().Interface().(*timestamppb.Timestamp)
		if !ok {
			return ""
		}
		return stamp.AsTime().UTC().Format(time.RFC3339Nano)
	case Commas, Lines:
		items := make([]string, v.List().Len())
		for i := range items {
			items[i] = v.List().Get(i).String()
		}
		separator := ", "
		if column.Format == Lines {
			separator = "\n"
		}
		return strings.Join(items, separator)
	case Text:
		return fmt.Sprint(v.Interface())
	default:
		return ""
	}
}

// Write always preserves complete JSON, independently of table definitions.
func Write(w io.Writer, format string, response proto.Message) error {
	return WriteColumns(w, format, response, "", false)
}

func WriteColumns(w io.Writer, format string, response proto.Message, columns string, selected bool) error {
	if format == clioutput.JSON {
		data, err := protojson.Marshal(response)
		if err != nil {
			return err
		}
		n, err := w.Write(append(data, '\n'))
		if err == nil && n != len(data)+1 {
			return io.ErrShortWrite
		}
		return err
	}
	v, err := responseView(response, columns, selected)
	if err != nil {
		return err
	}
	return v.Write(w)
}

func responseView(response proto.Message, columns string, selected bool) (clioutput.View, error) {
	message := response.ProtoReflect()
	if err := Validate(response, columns, selected); err != nil {
		return clioutput.View{}, err
	}
	d, ok := definitions[message.Descriptor().FullName()]
	if !ok {
		// The only non-protobuf workflow response is a typed deletion confirmation.
		if confirmation, isStruct := response.(*structpb.Struct); isStruct && confirmation.GetFields()["deleted"].GetBoolValue() {
			return clioutput.Details([]string{"Instance UUID", confirmation.GetFields()["instanceId"].GetStringValue()}, []string{commandUUIDLabel, confirmation.GetFields()["commandId"].GetStringValue()}, []string{"Deleted", "true"}), nil
		}
		data, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(response)
		return clioutput.View{Sections: []clioutput.Section{{Name: string(message.Descriptor().Name()), JSON: data}}}, err
	}
	if selected {
		var projectionErr error
		d.Fields, projectionErr = project(d.Fields, columns)
		if projectionErr != nil {
			return clioutput.View{}, projectionErr
		}
	}
	v := clioutput.View{Empty: d.Empty}
	rows := []protoreflect.Message{message}
	if d.Rows != "" {
		list, _ := value(message, d.Rows)
		rows = make([]protoreflect.Message, list.List().Len())
		for i := range rows {
			rows[i] = list.List().Get(i).Message()
		}
		for _, f := range d.Fields {
			v.Headers = append(v.Headers, f.Header)
		}
	} else {
		v.Headers = []string{"Field", "Value"}
	}
	for _, row := range rows {
		cells := make([]string, 0, len(d.Fields))
		for _, f := range d.Fields {
			text := cell(row, f)
			if d.Rows == "" {
				v.Rows = append(v.Rows, []string{f.Header, text})
			} else {
				cells = append(cells, text)
			}
		}
		if d.Rows != "" {
			v.Rows = append(v.Rows, cells)
		}
		for _, section := range d.Sections {
			nested, _ := value(row, section.Path)
			if !nested.Message().IsValid() {
				continue
			}
			data, err := protojson.Marshal(nested.Message().Interface())
			if err != nil {
				return v, err
			}
			v.Sections = append(v.Sections, clioutput.Section{Name: section.Header, JSON: data})
		}
	}
	if d.Failure != "" {
		failure, _ := value(message, d.Failure)
		if failure.Message().IsValid() {
			for _, key := range []string{"code", "message", "path"} {
				v.Rows = append(v.Rows, []string{"Error " + key, cell(failure.Message(), Field{Path: key})})
			}
		}
	}
	if d.NextCursor != "" {
		v.NextCursor = cell(message, Field{Path: d.NextCursor})
	}
	if d.ObservedAt != "" {
		v.ObservedAt = cell(message, Field{Path: d.ObservedAt, Format: Timestamp})
	}
	return v, nil
}

// ValidateDefinitions catches stale views, including selected catalog details,
// before the generated command can contact the daemon.
func ValidateDefinitions() error {
	for name, definition := range definitions {
		message, err := protoregistry.GlobalTypes.FindMessageByName(name)
		if err != nil {
			return errors.New("presentation response descriptor is missing")
		}
		if validationErr := definition.Validate(message.Descriptor()); validationErr != nil {
			return validationErr
		}
	}
	return nil
}
