// Original: https://github.com/pborman/getopt/blob/v2.1.0/v2/counter.go
//
// License: https://github.com/pborman/getopt/blob/v2.1.0/LICENSE
//
// Portions Copyright 2017 Google Inc.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found at the link above.

package getoptx

import (
	"fmt"
	"strconv"

	"github.com/pborman/getopt/v2"
)

// Counter counts the number of times the given flag has
// been set from the command line.
type Counter int

// Set implements the Value.Set method.
func (b *Counter) Set(value string, opt getopt.Option) error {
	if value == "" {
		*b++
	} else {
		val, err := strconv.ParseInt(value, 0, strconv.IntSize)
		if err != nil {
			if e, ok := err.(*strconv.NumError); ok {
				switch e.Err {
				case strconv.ErrRange:
					err = fmt.Errorf("value out of range: %s", value)
				case strconv.ErrSyntax:
					err = fmt.Errorf("not a valid number: %s", value)
				}
			}
			return err
		}
		*b = Counter(val)
	}
	return nil
}

// String implements the Value.String method.
func (b *Counter) String() string {
	return strconv.Itoa(int(*b))
}
