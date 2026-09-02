package shlex

import (
	"reflect"
	"testing"
)

func TestSplitWindowsPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want []string
	}{
		{`C:\Users\me\editors\my_editor.exe --wait`, []string{`C:\Users\me\editors\my_editor.exe`, "--wait"}},
		{`"C:\Program Files\Notepad++\notepad++.exe"`, []string{`C:\Program Files\Notepad++\notepad++.exe`}},
		{`"C:\Program Files\my editor.exe" -f`, []string{`C:\Program Files\my editor.exe`, "-f"}},
	}

	for _, tt := range tests {
		got, err := SplitWindows(tt.in)
		if err != nil {
			t.Errorf("Split(%q) failed: %v", tt.in, err)

			continue
		}

		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Split(%q) = %#v, want %#v", tt.in, got, tt.want)
		}
	}
}
