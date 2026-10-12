package app

import (
	"sort"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var protoUnmarshaler = protojson.UnmarshalOptions{DiscardUnknown: true}

func protoUnmarshalJSON(b []byte, m proto.Message) error { return protoUnmarshaler.Unmarshal(b, m) }

func sortBy[T any](s []T, key func(T) string) {
	sort.SliceStable(s, func(i, j int) bool { return key(s[i]) < key(s[j]) })
}
