// Credit: https://stackoverflow.com/a/62454413/451664

package util

type CopyableMap map[string]interface{}

func (this CopyableMap) DeepCopy() map[string]interface{} {
	result := map[string]interface{}{}

	for k, v := range this {
		mapVal, ok := v.(map[string]interface{})
		if ok {
			result[k] = CopyableMap(mapVal).DeepCopy()
			continue
		}

		sliceVal, ok := v.([]interface{})
		if ok {
			result[k] = CopyableSlice(sliceVal).DeepCopy()
			continue
		}

		result[k] = v
	}

	return result
}

type CopyableSlice []interface{}

func (this CopyableSlice) DeepCopy() []interface{} {
	result := []interface{}{}

	for _, v := range this {
		mapVal, ok := v.(map[string]interface{})
		if ok {
			result = append(result, CopyableMap(mapVal).DeepCopy())
			continue
		}

		sliceVal, ok := v.([]interface{})
		if ok {
			result = append(result, CopyableSlice(sliceVal).DeepCopy())
			continue
		}

		result = append(result, v)
	}

	return result
}
