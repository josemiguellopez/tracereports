//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	tr "github.com/josemiguellopez/tracereports/client/go"
)

func main() {
	c := tr.New("")
	c.StartRun("ÓrangeHRM / PIM", "")
	test, _ := c.StartTest("local HTML", "", "")
	test.Info("renderer evidence")
	test.Finish(tr.Pass, "", "")
	c.FinishRun()
	result, _ := json.Marshal(map[string]any{"report": c.OfflineReport, "url": c.ReportURL(), "run": c.RunID})
	fmt.Println("RESULT " + string(result))
}
