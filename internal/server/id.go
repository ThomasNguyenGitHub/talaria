// Copyright 2019 Grabtaxi Holdings PTE LTE (GRAB), All rights reserved.
// Use of this source code is governed by an MIT-style license that can be found in the LICENSE file

package server

import (
	"fmt"
	"reflect"

	"github.com/grab/talaria/internal/presto"
	"github.com/kelindar/binary"
)

// thriftID represents a split key along with the table name
type thriftID struct {
	Table string // The name of the table
	Split []byte // The encoded split key
}

// encodeID creates a split ID by encoding a query.
func encodeID(table string, split []byte) *presto.PrestoThriftId {
	b, err := binary.Marshal(&thriftID{
		Table: table,
		Split: split,
	})
	if err != nil {
		panic(err)
	}

	return &presto.PrestoThriftId{
		Id: b,
	}
}

// decodeID unmarshals thrift ID back to a struct.
func decodeID(id *presto.PrestoThriftId, token *presto.PrestoThriftNullableToken) (out *thriftID, err error) {
	if token.Token != nil {
		id = token.Token
	}

	out = new(thriftID)
	err = binary.Unmarshal(id.Id, out)
	return
}

// ------------------------------------------------------------------------------------------------------------

// Converts reflect type to SQL type
func toSQLType(t reflect.Type) string {
	switch t.Name() {
	case "string":
		return "varchar"
	case "int32":
		return "int"
	case "int64":
		return "bigint"
	case "float64":
		return "double"
	}

	panic(fmt.Errorf("sql type for %v is not found, did you forget to add it", t.Name()))
}