package workqueue

// Abstract work
type Work func()

// A queue that uses a limited amount of worker porcesses
type WorkQueue struct {
	work chan Work
	stop chan int
}

// Queue new work to be executed in the queue, block if full
func (self *WorkQueue) AddBlocking(work Work) {
	self.work <- work
}

// Queue new work to be executed in the queue, return if full
func (self *WorkQueue) AddIfSpace(work Work) bool {
	select {
	case self.work <- work:
		return true
	default:
		return false
	}
}

// Abandon all the work in the queue and stop working
func (self *WorkQueue) Cancel() {
	self.stop <- 1
}

// The main work loop
func worker(workQueue *WorkQueue) {
	for {
		select {
		case work := <-workQueue.work:
			work()
		case <-workQueue.stop:
			return
		}
	}
}

// Create a new WorkQueue
// workerCount: Number of concurrent processes to use
func New(workerCount int) *WorkQueue {
	workQueue := &WorkQueue{
		work: make(chan Work, 1024),
		stop: make(chan int),
	}

	for i := 0; i < workerCount; i++ {
		go worker(workQueue)
	}

	return workQueue
}
