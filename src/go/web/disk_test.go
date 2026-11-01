package web

// diskVM is a topology node, a VM booting from the images.
func diskVM(name string, images ...string) map[string]any {
	drives := make([]map[string]any, 0, len(images))
	for _, image := range images {
		drives = append(drives, map[string]any{"image": image})
	}

	return map[string]any{
		"type":     "VirtualMachine",
		"general":  map[string]any{"hostname": name},
		"hardware": map[string]any{"vcpus": 1, "memory": 512, "os_type": "linux", "drives": drives},
	}
}
