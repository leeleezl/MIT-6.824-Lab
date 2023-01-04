## Raft 简介
Raft 是一种为了管理复制日志的一致性算法，它拥有与 Paxos 相同的功能和性能，但是算法结构与 Paxos 不同，Raft 将一致性算法分成了几个关键的模块，例如领导人选举，日志复制和安全性，在Lab2A 中我们实现的是领导人选举。
## 领导人选举
Raft 通过选举一个领导人，领导人拥管理日志复制的全部责任来实现一致性。领导人从客户端接受日志条目，然后把日志条目复制到其他服务器上，并且在保证安全性的情况下告诉其他服务器将日志条目应用到各自的状态机上。一个领导人可以发生宕机，当发生宕机时，一个新的领导人就会被选举出来。
根据 Raft 协议，一个应用 Raft 协议的集群在刚启动时，所有节点的状态都是 Follower。由于没有 Leader，Followers 无法与 Leader 保持心跳（Heart Beat），因此，Followers 会认为 Leader 已经下线，进而转为 Candidate 状态。然后，**Candidate 将向集群中其它节点请求投票，同意自己升级为 Leader。如果 Candidate 收到超过半数节点的投票（N/2 + 1），它将获胜成为 Leader。**
## 选举逻辑实现
第一阶段：所有的节点都是Follower
一个 Raft 集群刚启动时，所有的节点状态都是 Follower，初始 Term 为0。同时启动选举定时器，选举定时器的超时时间在200到350毫秒内（避免同时发生选举）。
```go
timeout := time.Duration(200+rand.Int31n(150)) * time.Millisecond // 选举定时器随机化，防止各个节点同时发生选举
```
第二阶段：Follower 转为 Candidate 并发起投票
没有 Leader，Followers 无法与 Leader 保持心跳，节点启动后，在一个选举定时器周期内，没有收到心跳和投票请求，则转换自身角色为 Candidate，且 Term 自增，并向集群中的所有节点发送投票并重置选举定时器。
- 选举定时器超时，转变角色
```go
//选举定时器超时，转变角色
elapses := now.Sub(rf.lastActiveTime)
// follower -> candidates
if rf.role == ROLE_FOLLOWER {
	if elapses >= timeout { //发生超时
		rf.role = ROLE_CANDIDATES //转换角色
		DPrintf("RaftNode[%d] Follower -> Candidate", rf.me)
	}
}
```
- 启动多个协程并发发起投票
```go
// 发起投票请求
if rf.role == ROLE_CANDIDATES && elapses >= timeout {
	rf.lastActiveTime = now // 重置下次选举时间

	rf.currentTerm += 1 // 发起新任期
	rf.votedFor = rf.me // 该任期投了自己
	rf.persist()

	// 请求投票req
	args := RequestVoteArgs{
		Term:         rf.currentTerm,
		CandidateId:  rf.me,
		LastLogIndex: len(rf.log),
	}
	if len(rf.log) != 0 {
		args.LastLogTerm = rf.log[len(rf.log)-1].Term
	}

	rf.mu.Unlock()

	DPrintf("RaftNode[%d] RequestVote starts, Term[%d] LastLogIndex[%d] LastLogTerm[%d]", rf.me, args.Term,
		args.LastLogIndex, args.LastLogTerm)

	// 并发RPC请求vote
	type VoteResult struct {
		peerId int
		resp   *RequestVoteReply
	}
	voteCount := 1   // 收到投票个数（先给自己投1票）
	finishCount := 1 // 收到应答个数
	voteResultChan := make(chan *VoteResult, len(rf.peers))
	//多个协程并发RPC请求
	for peerId := 0; peerId < len(rf.peers); peerId++ {
		go func(id int) {
			if id == rf.me {
				return
			}
			resp := RequestVoteReply{}
			if ok := rf.sendRequestVote(id, &args, &resp); ok {
				voteResultChan <- &VoteResult{peerId: id, resp: &resp}
			} else {
				voteResultChan <- &VoteResult{peerId: id, resp: nil}
			}
		}(peerId)
	}
```
第三阶段：投票策略
节点收到投票请求后，会根据以下情况决定是否接受投票请求（每个 follower 刚成为 Candidate 的时候会将票投给自己）
> 请求节点的 Term 大于自己的 Term，且自己尚未投票给其它节点，则接受请求，把票投给它；
> 请求节点的 Term 小于自己的 Term，且自己尚未投票，则拒绝请求，将票投给自己。
- 变为 Candidate 时将选票投给自己
```go
voteCount := 1   // 收到投票个数（先给自己投1票）
finishCount := 1 // 收到应答个数
```
- 节点投票的策略
```go
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).
	rf.mu.Lock()
	defer rf.mu.Unlock()

	reply.Term = rf.currentTerm
	reply.VoteGranted = false

	//任期不如我大，拒绝投票
	if args.Term < rf.currentTerm {
		return
	}

	//发现更大的任期，则转为该任期的follower
	if args.Term > rf.currentTerm {
		rf.currentTerm = args.Term
		rf.role = ROLE_FOLLOWER
		rf.votedFor = -1
		rf.leaderId = -1
		//继续向下走进行投票
	}

	//每个任期只能投票给一个人
	if rf.votedFor == -1 || rf.votedFor == args.CandidateId {
		//candidate的日志必须比我新
		//1. 最后一条log，任期大的更新
		//2. 更长的log则更新
		lastLogTerm := 0
		if len(rf.log) != 0 {
			lastLogTerm = rf.log[len(rf.log)-1].Term
		}
		if args.LastLogTerm < lastLogTerm || args.LastLogIndex < len(rf.log) {
			return
		}
		rf.votedFor = args.CandidateId
		reply.VoteGranted = true
		rf.lastActiveTime = time.Now()
	}
	rf.persist()
}
```
第四阶段：Candidate 成为 Leader
一轮选举过后，正常情况下，会有一个 Candidate 收到超过半数节点（N/2 + 1）的投票，它将胜出并升级为 Leader。然后定时发送心跳给其它的节点，其它节点会转为 Follower 并与 Leader 保持同步，到此，本轮选举结束。
- 正常情况收到（N/2 + 1）的投票
```go
for {
	select {
	case voteResult := <-voteResultChan:
		finishCount += 1
		if voteResult.resp != nil {
			if voteResult.resp.VoteGranted {
				voteCount += 1
			}
			if voteResult.resp.Term > maxTerm {
				maxTerm = voteResult.resp.Term
			}
		}
		// 得到大多数vote后，立即离开
		if finishCount == len(rf.peers) || voteCount > len(rf.peers)/2 {
			goto VOTE_END
		}
	}
}
```
- 结束时异常处理
	- 角色改变，结束本轮投票
	- 发现更高的 Term，则切换回 Follower
```go
VOTE_END:
	rf.mu.Lock()
	// 如果角色改变了，则忽略本轮投票结果
	if rf.role != ROLE_CANDIDATES {
		return
	}
	// 发现了更高的任期，切回follower
	if maxTerm > rf.currentTerm {
		rf.role = ROLE_FOLLOWER
		rf.leaderId = -1
		rf.currentTerm = maxTerm
		rf.votedFor = -1
		rf.persist()
		return
	}
	// 赢得大多数选票，则成为leader
	if voteCount > len(rf.peers)/2 {
		rf.role = ROLE_LEADER
		rf.leaderId = rf.me
		rf.lastBroadcastTime = time.Unix(0, 0) // 令appendEntries广播立即执行
		return
	}
}
}()
```
## 心跳机制实现
**lab2A 只考虑心跳，不考虑 log 的同步**
Raft 集群在正常运行中是不会触发选举的，选举只会发生在集群初次启动或者其它节点无法收到Leader 心跳的情况下。初次启动比较好理解，因为raft节点在启动时，默认都是将自己设置为Follower。收不到 Leader 心跳有两种情况，一种是原来的 Leader 机器 Crash 了，还有一种是发生网络分区，Follower 跟 Leader 之间的网络断了，Follower 以为 Leader 宕机了。
我们在lab2A中日志同步和心跳机制复用一个方法，但是在lab2A中不考虑日志同步的情况，因此 Leader 广播的消息中日志部分为空则代表是心跳消息。我们的广播周期为100ms。
```go
func (rf *Raft) appendEntriesLoop() {
	for !rf.killed() {
		time.Sleep(1 * time.Millisecond)

		func() {
			rf.mu.Lock()
			defer rf.mu.Unlock()

			// 只有leader才向外广播心跳
			if rf.role != ROLE_LEADER {
				return
			}

			// 100ms广播1次
			now := time.Now()
			if now.Sub(rf.lastBroadcastTime) < 100*time.Millisecond {
				return
			}
			rf.lastBroadcastTime = time.Now()

			// 并发RPC心跳
			type AppendResult struct {
				peerId int
				resp   *AppendEntriesReply
			}

			for peerId := 0; peerId < len(rf.peers); peerId++ {
				if peerId == rf.me {
					continue
				}

				args := AppendEntriesArgs{}
				args.Term = rf.currentTerm
				args.LeaderId = rf.me
				// log相关字段在lab-2A不处理
				go func(id int, args1 *AppendEntriesArgs) {
					DPrintf("RaftNode[%d] appendEntries starts, myTerm[%d] peerId[%d]", rf.me, args1.Term, id)
					reply := AppendEntriesReply{}
					if ok := rf.sendAppendEntries(id, args1, &reply); ok {
						rf.mu.Lock()
						defer rf.mu.Unlock()
						if reply.Term > rf.currentTerm { // 变成follower
							rf.role = ROLE_FOLLOWER
							rf.leaderId = -1
							rf.currentTerm = reply.Term
							rf.votedFor = -1
							rf.persist()
						}
						DPrintf("RaftNode[%d] appendEntries ends, peerTerm[%d] myCurrentTerm[%d] myRole[%s]", rf.me, reply.Term, rf.currentTerm, rf.role)
					}
				}(peerId, &args)
			}
		}()
	}
}
```
## 总结
1. 每次请求和响应时，都要先判断，如果 term > currentTerm，要转换角色为 Follower
2. 每个 term 只能 voteFor 其他节点一次
3. candidates 请求投票时间是随机的，注意随机性
4. 得到大多数选票后立即结束等待剩余RPC
5. 成为 Leader 后要尽快进行心跳，否则其他节点又将变成 Candidate