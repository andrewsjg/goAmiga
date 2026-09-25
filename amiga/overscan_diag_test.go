package amiga

import "testing"

// TestOverscanScreenshot boots Workbench and saves the overscan view to
// /tmp/workbench_overscan.png:
//
//	GOAMIGA_DIAG=1 go test -run TestOverscanScreenshot ./amiga/
func TestOverscanScreenshot(t *testing.T) {
	requireDiag(t)
	a := bootWB(t)
	a.Video.SetOverscan(true)
	a.Video.DisplayActive = true
	for i := 0; i < 3; i++ {
		if err := a.RunFrame(); err != nil {
			t.Fatal(err)
		}
	}
	saveScreenshot(a.Video, "/tmp/workbench_overscan.png")
	t.Logf("wrote /tmp/workbench_overscan.png (%dx%d)", a.Video.Width, a.Video.Height)
}
