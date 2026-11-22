// Package fanout exercises repeated static call contexts without runtime execution.
package fanout

import "os"

func publish() {
	_ = os.Rename("state.tmp", "state")
}

func level01() { publish(); publish() }
func level02() { level01(); level01() }
func level03() { level02(); level02() }
func level04() { level03(); level03() }
func level05() { level04(); level04() }
func level06() { level05(); level05() }
func level07() { level06(); level06() }
func level08() { level07(); level07() }
func level09() { level08(); level08() }
func level10() { level09(); level09() }
func level11() { level10(); level10() }
func level12() { level11(); level11() }

func Publish() {
	level12()
}
