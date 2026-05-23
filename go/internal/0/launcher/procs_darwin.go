package launcher

import (
	"golang.org/x/sys/unix"
)

// collectDescendants returns every descendant PID of root, using the
// Darwin sysctl kern.proc.all interface. Iterates every process in the
// system once, builds a ppid -> []pid map, then BFS from root.
//
// Darwin has no /proc, so the Linux task/<tid>/children walk does not
// apply. libproc's proc_listpids(PROC_PPID_ONLY, ...) would be more
// targeted but requires cgo; kern.proc.all is cheap enough in practice
// (thousands of processes, one syscall) and keeps the launcher
// cgo-free.
func collectDescendants(root int) []int {
	kprocs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil
	}

	childrenByPpid := make(map[int][]int, len(kprocs))
	for i := range kprocs {
		pid := int(kprocs[i].Proc.P_pid)
		ppid := int(kprocs[i].Eproc.Ppid)
		childrenByPpid[ppid] = append(childrenByPpid[ppid], pid)
	}

	var result []int
	seen := make(map[int]bool)
	queue := []int{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range childrenByPpid[cur] {
			if seen[child] {
				continue
			}
			seen[child] = true
			result = append(result, child)
			queue = append(queue, child)
		}
	}
	return result
}
