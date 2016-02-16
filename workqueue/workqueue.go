package workqueue

// Abstract work
type Work func()

// A queue that uses a limited amount of worker porcesses
type WorkQueue struct {
	work chan Work
	stop chan int
}

// Queue new work to be executed in the queue
func (self *WorkQueue) Add(work Work) {
	self.work <- work
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
	workQueue := new(WorkQueue)
	workQueue.work = make(chan Work, 1024)
	workQueue.stop = make(chan int)

	for i := 0; i < workerCount; i++ {
		go worker(workQueue)
	}

	return workQueue
}
