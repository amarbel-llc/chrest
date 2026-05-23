package launcher

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// collectDescendants returns every descendant PID of root, using Linux's
// /proc/<pid>/task/<tid>/children interface. Requires CONFIG_PROC_CHILDREN
// (default-y on recent kernels).
func collectDescendants(root int) []int {
	var result []int
	seen := make(map[int]bool)

	var walk func(int)
	walk = func(pid int) {
		tasks, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
		if err != nil {
			return
		}
		for _, task := range tasks {
			data, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%s/children", pid, task.Name()))
			if err != nil {
				continue
			}
			for _, s := range strings.Fields(string(data)) {
				child, err := strconv.Atoi(s)
				if err != nil || seen[child] {
					continue
				}
				seen[child] = true
				result = append(result, child)
				walk(child)
			}
		}
	}
	walk(root)
	return result
}
