package scorchexe

import (
	"fmt"
	"slices"
	"sort"

	"phenix/api/scorch/scorchmd"
)

// Claims last through cleanup, including breakpoints. Unrestricted minimega
// commands and unknown components conservatively require namespace exclusivity.
func resources(md scorchmd.ScorchMetadata, run int) []string {
	claims := make(map[string]bool)
	for loop := md.Runs[run]; loop != nil; loop = loop.Loop {
		for _, stage := range [][]string{loop.Configure, loop.Start, loop.Stop, loop.Cleanup} {
			for _, name := range stage {
				spec := md.ComponentSpecs()[name]
				switch spec.Type {
				case componentBreak, "pause", "soh", "tap", "cc", "vmstats":
				case "tcpdump", "snort", "ettercap", "iperf":
					claims[spec.Type] = true
				default:
					claims["*"] = true
				}
			}
		}
	}
	result := make([]string, 0, len(claims))
	for key := range claims {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func checkResources(claims []string, s *scorchmd.ScorchStatus) error {
	for _, e := range s.Executions {
		if !e.Active() && e.State != scorchmd.StateInterrupted {
			continue
		}
		for _, owned := range e.Resources {
			if owned == "*" {
				return fmt.Errorf("%w: run %d owns the namespace", ErrResourceConflict, e.Run)
			}
			for _, requested := range claims {
				if requested == "*" || requested == owned {
					return fmt.Errorf("%w: run %d owns %s", ErrResourceConflict, e.Run, owned)
				}
			}
		}
		if slices.Contains(claims, "*") {
			return fmt.Errorf("%w: run %d is active", ErrResourceConflict, e.Run)
		}
	}
	return nil
}
