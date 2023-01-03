package mr

import (
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"sync"
	"time"
)

type Coordinator struct {
	// Your definitions here.
	mu              sync.Mutex
	MapperFinished  bool           // mapper是否全部完成
	ReducerFinished bool           // reducer是否全部完成
	Mappers         []*MapperTask  // mapper任务
	Reducers        []*ReducerTask // reducer任务
}

type MapperTask struct {
	Index        int       // 任务编号
	Assigned     bool      // 是否分配
	AssignedTime time.Time // 分配时间
	IsFinished   bool      // 是否完成

	InputFile    string // 输入文件
	ReducerCount int    // 有多少路reducer

	timeoutTimer *time.Timer // 任务超时
}

type ReducerTask struct {
	Index        int       // 任务编号
	Assigned     bool      // 是否分配
	AssignedTime time.Time // 分配时间
	IsFinished   bool      // 是否完成

	MapperCount int //	有多少路mapper

	timeoutTimer *time.Timer // 任务超时
}

// Your code here -- RPC handlers for the worker to call.

//执行map任务
func (c *Coordinator) startMapper(mapper *MapperTask) {
	mapper.Assigned = true
	mapper.AssignedTime = time.Now()
	mapper.timeoutTimer = time.AfterFunc(10*time.Second, func(index int) func() {
		return func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if !c.Mappers[index].IsFinished {
				c.Mappers[index].Assigned = false
			}
		}
	}(mapper.Index))
}

//执行reduce任务
func (c *Coordinator) startReducer(reducer *ReducerTask) {
	reducer.Assigned = true
	reducer.AssignedTime = time.Now()
	reducer.timeoutTimer = time.AfterFunc(10*time.Second, func(index int) func() {
		return func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if !c.Reducers[index].IsFinished {
				c.Reducers[index].Assigned = false
			}
		}
	}(reducer.Index))
}

func (m *Coordinator) finishMapper(mapper *MapperTask) {
	mapper.IsFinished = true
	mapper.timeoutTimer.Stop()
}

func (m *Coordinator) finishReducer(reducer *ReducerTask) {
	reducer.IsFinished = true
	reducer.timeoutTimer.Stop()
}

//拉取任务
func (c *Coordinator) FetchTask(request *FetchTaskRequest, response *FetchTaskResponse) (err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.MapperFinished { //如果mapper没完成
		for _, mapper := range c.Mappers { //遍历mappers任务列表
			if mapper.Assigned || mapper.IsFinished {
				continue
			}
			c.startMapper(mapper)
			task := *mapper // 副本
			response.MapperTask = &task
			return
		}
		return // 所有mapper任务都分配出去了，那么暂时没有工作了
	}
	if !c.ReducerFinished {
		for _, reducer := range c.Reducers {
			if reducer.Assigned || reducer.IsFinished {
				continue
			}
			c.startReducer(reducer)
			task := *reducer
			response.ReducerTask = &task
			return
		}
		return // 所有reducer任务都分配出去了，那么暂时没有工作了
	}
	response.AllFinished = true
	return
}

//更新任务状态
func (c *Coordinator) UpdateTask(request *UpdateTaskRequest, response *UpdateTaskResponse) (err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if request.Mapper != nil {
		MapperFinished := true
		for _, mapper := range c.Mappers {
			if mapper.Index == request.Mapper.Index && mapper.Assigned && !mapper.IsFinished {
				c.finishMapper(mapper)
			}
			MapperFinished = MapperFinished && mapper.IsFinished
		}
		c.MapperFinished = MapperFinished //检查mapper任务是否全部完成
	}
	if request.Reducer != nil {
		ReducerFinished := true
		for _, reducer := range c.Reducers {
			if reducer.Index == request.Reducer.Index && reducer.Assigned && !reducer.IsFinished {
				c.finishReducer(reducer)
			}
			ReducerFinished = ReducerFinished && reducer.IsFinished
		}
		c.ReducerFinished = ReducerFinished //检查reducer任务是否完成
	}
	return
}

//
// an example RPC handler.
//
// the RPC argument and reply types are defined in rpc.go.
//
func (c *Coordinator) Example(args *ExampleArgs, reply *ExampleReply) error {
	reply.Y = args.X + 1
	return nil
}

//
// start a thread that listens for RPCs from worker.go
//
func (c *Coordinator) server() {
	rpc.Register(c)
	rpc.HandleHTTP()
	//l, e := net.Listen("tcp", ":1234")
	sockname := coordinatorSock()
	os.Remove(sockname)
	l, e := net.Listen("unix", sockname)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go http.Serve(l, nil)
}

//
// main/mrcoordinator.go calls Done() periodically to find out
// if the entire job has finished.
//
func (c *Coordinator) Done() bool {

	// Your code here.
	ret := false

	// Your code here.
	ret = c.MapperFinished && c.ReducerFinished

	return ret
}

//
// create a Coordinator.
// main/mrcoordinator.go calls this function.
// nReduce is the number of reduce tasks to use.
//
func MakeCoordinator(files []string, nReduce int) *Coordinator {
	c := Coordinator{}

	// Your code here.

	// 1, Mapper任务列表
	c.Mappers = make([]*MapperTask, 0)
	for i, file := range files {
		mapper := &MapperTask{
			Index:        i,
			Assigned:     false,
			AssignedTime: time.Now(),
			IsFinished:   false,
			InputFile:    file,
			ReducerCount: nReduce,
		}
		c.Mappers = append(c.Mappers, mapper)
	}

	// 2，Reducer任务列表
	c.Reducers = make([]*ReducerTask, 0)
	for i := 0; i < nReduce; i++ {
		reducer := &ReducerTask{
			Index:        i,
			Assigned:     false,
			AssignedTime: time.Now(),
			IsFinished:   false,
			MapperCount:  len(files),
		}
		c.Reducers = append(c.Reducers, reducer)
	}

	c.server()
	return &c
}
