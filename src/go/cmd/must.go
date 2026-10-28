package cmd

import "fmt"

// mustGet returns a flag's value from a pflag getter such as
// cmd.Flags().GetString, panicking when the flag is undefined or mistyped.
func mustGet[T any](get func(string) (T, error), name string) T { //nolint:ireturn // T is concrete at each use
	val, err := get(name)
	if err != nil {
		panic(fmt.Sprintf("Getting value for %s: %v", name, err))
	}

	return val
}
