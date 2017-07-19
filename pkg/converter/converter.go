package converter

type converter interface{

	// Converts kubernetes object to calico representation of it.
	convert(k8sObj interface{})(interface{},error)
}