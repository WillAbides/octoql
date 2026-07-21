package generate

import "testing"

type stringFuncTest struct {
	name string
	in   string
	out  string
}

func testStringFunc(t *testing.T, f func(string) string, tests []stringFuncTest) {
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := f(test.in)
			if got != test.out {
				t.Errorf("got %#v want %#v", got, test.out)
			}
		})
	}
}

func TestUpperFirst(t *testing.T) {
	tests := []stringFuncTest{
		{"Empty", "", ""},
		{"SingleLower", "l", "L"},
		{"SingleUpper", "L", "L"},
		{"SingleUnicodeLower", "ļ", "Ļ"},
		{"SingleUnicodeUpper", "Ļ", "Ļ"},
		{"LongerLower", "lasdf", "Lasdf"},
		{"LongerUpper", "Lasdf", "Lasdf"},
		{"LongerUnicodeLower", "ļasdf", "Ļasdf"},
		{"LongerUnicodeUpper", "Ļasdf", "Ļasdf"},
	}

	testStringFunc(t, upperFirst, tests)
}

func TestGoConstName(t *testing.T) {
	tests := []stringFuncTest{
		{"Empty", "", ""},
		{"AllCaps", "ASDF", "Asdf"},
		{"AllCapsWithUnderscore", "ASDF_GH", "AsdfGh"},
		{"JustUnderscore", "_", "_"},
		{"LeadingUnderscore", "_ASDF_GH", "AsdfGh"},
	}

	testStringFunc(t, goConstName, tests)
}
