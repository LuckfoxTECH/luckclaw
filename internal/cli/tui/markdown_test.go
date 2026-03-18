package tui

import "testing"

func TestRenderMarkdownSimple_NumberedListNoPanic(t *testing.T) {
	cases := []string{
		"1.",
		"1)",
		"1. a",
		"12) abc",
		"1. \n2.\n3) test",
	}
	for _, tc := range cases {
		_ = renderMarkdownSimple(tc, 80)
	}
}

