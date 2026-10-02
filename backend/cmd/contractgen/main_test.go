package main

import (
	"reflect"
	"testing"
)

func TestQuotedStringKeepsSwiftCompatibleAmpersands(t *testing.T) {
	if got, want := quotedString("About & Updates"), `"About & Updates"`; got != want {
		t.Fatalf("quoted string = %q, want %q", got, want)
	}
}

func TestContractTypesPreserveNullableAndRevisionValues(t *testing.T) {
	if got := swiftType(reflect.TypeOf((*string)(nil))); got != "String?" {
		t.Fatalf("nullable Swift string = %q", got)
	}
	if got := csharpType(reflect.TypeOf((*string)(nil))); got != "string?" {
		t.Fatalf("nullable C# string = %q", got)
	}
	revision := uint64(1<<63 + 1)
	if got := swiftScalarLiteral(reflect.ValueOf(revision)); got != "9223372036854775809" {
		t.Fatalf("Swift revision loses precision: %q", got)
	}
	if got := csharpScalarLiteral(reflect.ValueOf(revision)); got != "9223372036854775809UL" {
		t.Fatalf("C# revision loses precision: %q", got)
	}
	var missing *string
	if got := swiftScalarLiteral(reflect.ValueOf(missing)); got != "nil" {
		t.Fatalf("Swift absent asset = %q", got)
	}
	if got := csharpScalarLiteral(reflect.ValueOf(missing)); got != "null" {
		t.Fatalf("C# absent asset = %q", got)
	}
	asset := "owned-asset"
	if got := swiftScalarLiteral(reflect.ValueOf(&asset)); got != `"owned-asset"` {
		t.Fatalf("Swift selected asset = %q", got)
	}
}
