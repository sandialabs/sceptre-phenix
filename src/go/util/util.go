package util

import "os"

func MustHostname() string {
	name, err := os.Hostname()
	if err != nil {
		panic(err)
	}

	return name
}
