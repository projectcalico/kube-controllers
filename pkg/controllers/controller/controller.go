package controller

// Controller interface
type Controller interface {

	// Run method
	Run(threadiness int, stopCh chan struct{})
}
