package envsetup

import "testing"

func Test_appendLogToFile(t *testing.T) {
	t.Skip()
	err := appendLogToFile("test", "test")
	if err != nil {
		t.Errorf("error appending to file %s", err)
	}
}