// Copyright 2019-2020 Grabtaxi Holdings PTE LTE (GRAB), All rights reserved.
// Use of this source code is governed by an MIT-style license that can be found in the LICENSE file

package orc

import (
	"strings"

	"github.com/crphang/orc"
	"github.com/grab/talaria/internal/encoding/typeof"
)

// SchemaFor generates a schema
func SchemaFor(schema typeof.Schema) (*orc.TypeDescription, error) {
	var sb strings.Builder
	sb.WriteString("struct<")

	// Ensure keys are sorted
	schemaKey := schema.Columns()

	first := true
	for _, key := range schemaKey {
		typ := schema[key]
		if !first {
			sb.WriteByte(0x2c) // ,
		}
		first = false

		sb.WriteString(key)
		sb.WriteByte(0x3a) // :
		sb.WriteString(typ.Category().String())
	}

	sb.WriteByte(0x3e) // >
	return orc.ParseSchema(sb.String())
}
