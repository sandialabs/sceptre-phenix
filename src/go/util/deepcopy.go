// Credit: https://stackoverflow.com/a/62454413/451664

package util

type CopyableMap map[string]any

func (m CopyableMap) DeepCopy() map[string]any {
	result := map[string]any{}

	for k, v := range m {
		mapVal, ok := v.(map[string]any)
		if ok {
			result[k] = CopyableMap(mapVal).DeepCopy()

			continue
		}

		sliceVal, ok := v.([]any)
		if ok {
			result[k] = CopyableSlice(sliceVal).DeepCopy()

			continue
		}

		result[k] = v
	}

	return result
}

type CopyableSlice []any

func (s CopyableSlice) DeepCopy() []any {
	result := []any{}

	for _, v := range s {
		mapVal, ok := v.(map[string]any)
		if ok {
			result = append(result, CopyableMap(mapVal).DeepCopy())

			continue
		}

		sliceVal, ok := v.([]any)
		if ok {
			result = append(result, CopyableSlice(sliceVal).DeepCopy())

			continue
		}

		result = append(result, v)
	}

	return result
}
