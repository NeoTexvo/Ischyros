/*
Copyright IBM Corp. 2016 All Rights Reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

		 http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package pbft

import (
	"testing"
	"time"
  "strconv"
  "encoding/base64"
	"github.com/golang/protobuf/proto"
	"github.com/op/go-logging"

	"github.com/hyperledger/fabric/consensus/util/events"
	pb "github.com/hyperledger/fabric/protos"
)

func init() {
	logging.SetLevel(logging.DEBUG, "")
}

// TestReplicaCrash1 simulates the restart of replicas 0 and 1 after
// some state has been built (one request executed).  At the time of
// the restart, replica 0 is also the primary.  All three requests
// submitted should also be executed on all replicas.
func TestReplicaCrash1(t *testing.T) {
	validatorCount := 3
	config := loadConfig()
	config.Set("general.K", 2)
	config.Set("general.logmultiplier", 2)
	net := makePBFTNetwork(validatorCount, config)
	defer net.stop()

	net.pbftEndpoints[0].manager.Queue() <- createPbftReqBatch(1, uint64(generateBroadcaster(validatorCount)))
	net.process()

	for id := 0; id < 1; id++ {
		pe := net.pbftEndpoints[id]
		pe.pbft = newPbftCore(uint64(id), loadConfig(), pe.sc, events.NewTimerFactoryImpl(pe.manager))
		pe.manager.SetReceiver(pe.pbft)
		pe.pbft.N = 3
		pe.pbft.f = 1
		pe.pbft.K = 2
		pe.pbft.L = 2 * pe.pbft.K
	}

	net.pbftEndpoints[0].manager.Queue() <- createPbftReqBatch(2, uint64(generateBroadcaster(validatorCount)))
	net.pbftEndpoints[0].manager.Queue() <- createPbftReqBatch(3, uint64(generateBroadcaster(validatorCount)))
	net.process()

	for _, pep := range net.pbftEndpoints {
		if pep.sc.executions != 3 {
			t.Errorf("Expected 3 executions on replica %d, got %d", pep.id, pep.sc.executions)
			continue
		}

		if pep.pbft.view != 0 {
			t.Errorf("Replica %d should still be in view 0, is %v %d", pep.id, pep.pbft.activeView, pep.pbft.view)
		}
	}
}

// TestReplicaCrash2 is a misnomer.  It simulates a situation where
// one replica (#3) is byzantine and does not participate at all.
// Additionally, for view<2 and seqno=1, the network drops commit
// messages to all but replica 1.
func TestReplicaCrash2(t *testing.T) {
	validatorCount := 3
	config := loadConfig()
	config.Set("general.timeout.request", "800ms")
	config.Set("general.timeout.viewchange", "800ms")
	config.Set("general.K", 2)
	config.Set("general.logmultiplier", 2)
	net := makePBFTNetwork(validatorCount, config)
	defer net.stop()

	filterMsg := true
	net.filterFn = func(src int, dst int, msg []byte) []byte {
		if dst == 2 { // 3 is byz
			return nil
		}
		pm := &Message{}
		err := proto.Unmarshal(msg, pm)
		if err != nil {
			t.Fatal(err)
		}
		// filter commits to all but 1
		commit := pm.GetCommit()
		if filterMsg && dst != -1 && dst != 1 && commit != nil && commit.View < 2 {
			logger.Infof("filtering commit message from %d to %d", src, dst)
			return nil
		}
		return msg
	}

	net.pbftEndpoints[0].manager.Queue() <- createPbftReqBatch(1, uint64(generateBroadcaster(validatorCount)))
	net.process()

	logger.Info("stopping filtering")
	filterMsg = false
	primary := net.pbftEndpoints[0].pbft.primary(net.pbftEndpoints[0].pbft.view)
	net.pbftEndpoints[primary].manager.Queue() <- createPbftReqBatch(2, uint64(generateBroadcaster(validatorCount)))
	net.pbftEndpoints[primary].manager.Queue() <- createPbftReqBatch(3, uint64(generateBroadcaster(validatorCount)))
	net.pbftEndpoints[primary].manager.Queue() <- createPbftReqBatch(4, uint64(generateBroadcaster(validatorCount)))
	go net.processContinually()
	time.Sleep(5 * time.Second)

	for _, pep := range net.pbftEndpoints {
		if pep.id != 2 && pep.sc.executions != 4 {
			t.Errorf("Expected 4 executions on replica %d, got %d", pep.id, pep.sc.executions)
			continue
		}
		if pep.id == 2 && pep.sc.executions > 0 {
			t.Errorf("Expected no execution")
			continue
		}
	}
}

// TestReplicaCrash3 simulates the restart requiring a view change
// to a checkpoint which was restored from the persistence state
// Replicas 0,1,2 participate up to a checkpoint, then all crash
// Then replicas 0,1,3 start back up, and a view change must be
// triggered to get vp3 up to speed
func TestReplicaCrash3(t *testing.T) {
	validatorCount := 3
	config := loadConfig()
	config.Set("general.K", 2)
	config.Set("general.logmultiplier", 2)
	net := makePBFTNetwork(validatorCount, config)
	defer net.stop()

	twoOffline := false
	threeOffline := true
	net.filterFn = func(src int, dst int, msg []byte) []byte {
		if twoOffline && dst == 1 { // 2 is 'offline'
			return nil
		}
		if threeOffline && dst == 2 { // 3 is 'offline'
			return nil
		}
		return msg
	}

	for i := int64(1); i <= 8; i++ {
		net.pbftEndpoints[0].manager.Queue() <- createPbftReqBatch(i, uint64(generateBroadcaster(validatorCount)))
	}
	net.process() // vp0,1,2 should have a stable checkpoint for seqNo 8

  logger.Infof("Done phase 1")
	// Create new pbft instances to restore from persistence
	for id := 0; id < 1; id++ {
		pe := net.pbftEndpoints[id]
		config := loadConfig()
		config.Set("general.K", "2")
		pe.pbft.close()
		pe.pbft = newPbftCore(uint64(id), config, pe.sc, events.NewTimerFactoryImpl(pe.manager))
		pe.manager.SetReceiver(pe.pbft)
		pe.pbft.N = 4
		pe.pbft.f = (4 - 1) / 3
		pe.pbft.requestTimeout = 200 * time.Millisecond
	}

	threeOffline = false
	twoOffline = true

	// Because vp2 is 'offline', and vp3 is still at the genesis block, the network needs to make a view change

	net.pbftEndpoints[0].manager.Queue() <- createPbftReqBatch(9, uint64(generateBroadcaster(validatorCount)))
	net.process()

	// Now vp0,1,3 should be in sync with 9 executions in view 1, and vp2 should be at 8 executions in view 0
	for i, pep := range net.pbftEndpoints {

		if i == 1 {
			// 2 is 'offline'
			if pep.pbft.view != 0 {
				t.Errorf("Expected replica %d to be in view 0, got %d", pep.id, pep.pbft.view)
			}
			expectedExecutions := uint64(8)
			if pep.sc.executions != expectedExecutions {
				t.Errorf("Expected %d executions on replica %d, got %d", expectedExecutions, pep.id, pep.sc.executions)
			}
			continue
		}

		if pep.pbft.view != 3 {
			t.Errorf("Expected replica %d to be in view 3, got %d", pep.id, pep.pbft.view)
		}

		expectedExecutions := uint64(9)
		if pep.sc.executions != expectedExecutions {
			t.Errorf("Expected %d executions on replica %d, got %d", expectedExecutions, pep.id, pep.sc.executions)
		}
	}
}

// TestReplicaCrash4 simulates the restart with no checkpoints
// in the store because they have been garbage collected
// the bug occurs because the low watermark is incorrectly set to
// be zero
func TestReplicaCrash4(t *testing.T) {
	validatorCount := 3
	config := loadConfig()
	config.Set("general.K", 2)
	config.Set("general.logmultiplier", 2)
	net := makePBFTNetwork(validatorCount, config)
	defer net.stop()

	twoOffline := false
	threeOffline := true
	net.filterFn = func(src int, dst int, msg []byte) []byte {
		if twoOffline && dst == 1 { // 2 is 'offline'
			return nil
		}
		if threeOffline && dst == 2 { // 3 is 'offline'
			return nil
		}
		return msg
	}

	for i := int64(1); i <= 8; i++ {
		net.pbftEndpoints[0].manager.Queue() <- createPbftReqBatch(i, uint64(generateBroadcaster(validatorCount)))
	}
	net.process() // vp0,1,2 should have a stable checkpoint for seqNo 8
	net.process() // this second time is necessary for garbage collection it seams

	// Now vp0,1,2 should be in sync with 8 executions in view 0, and vp4 should be offline
	for i, pep := range net.pbftEndpoints {

		if i == 2 {
			// 3 is offline for this test
			continue
		}

		if pep.pbft.view != 0 {
			t.Errorf("Expected replica %d to be in view 1, got %d", pep.id, pep.pbft.view)
		}

		expectedExecutions := uint64(8)
		if pep.sc.executions != expectedExecutions {
			t.Errorf("Expected %d executions on replica %d, got %d", expectedExecutions, pep.id, pep.sc.executions)
		}
	}

	// Create new pbft instances to restore from persistence
	for id := 0; id < 2; id++ {
		pe := net.pbftEndpoints[id]
		config := loadConfig()
		config.Set("general.K", "2")
		pe.pbft.close()
		pe.pbft = newPbftCore(uint64(id), config, pe.sc, events.NewTimerFactoryImpl(pe.manager))
		pe.manager.SetReceiver(pe.pbft)
		pe.pbft.N = 3
		pe.pbft.f = (4 - 1) / 3
		pe.pbft.requestTimeout = 200 * time.Millisecond

		expected := uint64(8)
		if pe.pbft.h != expected {
			t.Errorf("Low watermark should have been %d, got %d", expected, pe.pbft.h)
		}
	}

}
/*
func TestReplicaPersistQSet(t *testing.T) {
	persist := make(map[string][]byte)

	stack := &omniProto{
		broadcastImpl: func(msg []byte) {
		},
		StoreStateImpl: func(key string, value []byte) error {
			persist[key] = value
			return nil
		},
		DelStateImpl: func(key string) {
			delete(persist, key)
		},
		ReadStateImpl: func(key string) ([]byte, error) {
			if val, ok := persist[key]; ok {
				return val, nil
			}
			return nil, fmt.Errorf("key not found")
		},
		ReadStateSetImpl: func(prefix string) (map[string][]byte, error) {
			r := make(map[string][]byte)
			for k, v := range persist {
				if len(k) >= len(prefix) && k[0:len(prefix)] == prefix {
					r[k] = v
				}
			}
			return r, nil
		},
	}
	p := newPbftCore(1, loadConfig(), stack, &inertTimerFactory{})
	reqBatch := createPbftReqBatch(1, 0)
	events.SendEvent(p, &PrePrepare{
		View:           0,
		SequenceNumber: 1,
		BatchDigest:    hash(reqBatch),
		RequestBatch:   reqBatch,
		ReplicaId:      uint64(0),
	})
	p.close()

	p = newPbftCore(1, loadConfig(), stack, &inertTimerFactory{})
	if !p.prePrepared(hash(reqBatch), 0, 1) {
		t.Errorf("did not restore qset properly")
	}
}
*/
func TestReplicaPersistDelete(t *testing.T) {
	persist := make(map[string][]byte)

	stack := &omniProto{
		StoreStateImpl: func(key string, value []byte) error {
			persist[key] = value
			return nil
		},
		DelStateImpl: func(key string) {
			delete(persist, key)
		},
	}
	p := newPbftCore(1, loadConfig(), stack, &inertTimerFactory{})
	p.reqBatchStore["a"] = &RequestBatch{}
	p.persistRequestBatch("a")
	if len(persist) != 1 {
		t.Error("expected one persisted entry")
	}
	p.persistDelRequestBatch("a")
	if len(persist) != 0 {
		t.Error("expected no persisted entry")
	}
}
func TestNilCurrentExec(t *testing.T) {
	p := newPbftCore(1, loadConfig(), &omniProto{}, &inertTimerFactory{})
	p.execDoneSync() // Per issue 1538, this would cause a Nil pointer dereference
}

func TestNetworkNullRequests(t *testing.T) {
	validatorCount := 3
	config := loadConfig()
	config.Set("general.timeout.nullrequest", "200ms")
	config.Set("general.timeout.request", "500ms")
	net := makePBFTNetwork(validatorCount, config)
	defer net.stop()

	net.pbftEndpoints[0].manager.Queue() <- createPbftReqBatch(1, 0)

	go net.processContinually()
	time.Sleep(3 * time.Second)

	for _, pep := range net.pbftEndpoints {
		if pep.sc.executions != 1 {
			t.Errorf("Instance %d executed incorrect number of transactions: %d", pep.id, pep.sc.executions)
		}
		if pep.pbft.lastExec <= 1 {
			t.Errorf("Instance %d: no null requests processed", pep.id)
		}
		if pep.pbft.view != 0 {
			t.Errorf("Instance %d: expected view=0", pep.id)
		}
	}
}

func TestNetworkNullRequestMissing(t *testing.T) {
	validatorCount := 4
	config := loadConfig()
	config.Set("general.timeout.nullrequest", "200ms")
	config.Set("general.timeout.request", "500ms")
	net := makePBFTNetwork(validatorCount, config)
	defer net.stop()

	net.pbftEndpoints[0].pbft.nullRequestTimeout = 0

	net.pbftEndpoints[0].manager.Queue() <- createPbftReqBatch(1, 0)

	go net.processContinually()
	time.Sleep(3 * time.Second) // Bumped from 2 to 3 seconds because of sporadic CI failures

	for _, pep := range net.pbftEndpoints {
		if pep.sc.executions != 1 {
			t.Errorf("Instance %d executed incorrect number of transactions: %d", pep.id, pep.sc.executions)
		}
		if pep.pbft.lastExec <= 1 {
			t.Errorf("Instance %d: no null requests processed", pep.id)
		}
		if pep.pbft.view != 1 {
			t.Errorf("Instance %d: expected view=1", pep.id)
		}
	}
}

func TestNetworkPeriodicViewChange(t *testing.T) {
	validatorCount := 3
	config := loadConfig()
	config.Set("general.K", "2")
	config.Set("general.logmultiplier", "2")
	config.Set("general.timeout.request", "500ms")
	config.Set("general.viewchangeperiod", "1")
	net := makePBFTNetwork(validatorCount, config)
	defer net.stop()

	for n := 1; n < 6; n++ {
		for _, pe := range net.pbftEndpoints {
			pe.manager.Queue() <- createPbftReqBatch(int64(n), 0)
		}
		net.process()
	}

	for _, pep := range net.pbftEndpoints {
		if pep.sc.executions != 5 {
			t.Errorf("Instance %d executed incorrect number of transactions: %d", pep.id, pep.sc.executions)
		}
		// We should be in view 2, 2 exec, VC, 2 exec, VC, exec
		if pep.pbft.view != 2 {
			t.Errorf("Instance %d: expected view=2", pep.id)
		}
	}
}

func TestNetworkPeriodicViewChangeMissing(t *testing.T) {
	validatorCount := 3
	config := loadConfig()
	config.Set("general.K", "2")
	config.Set("general.logmultiplier", "2")
	config.Set("general.timeout.request", "500ms")
	config.Set("general.viewchangeperiod", "1")
	net := makePBFTNetwork(validatorCount, config)
	defer net.stop()

	net.pbftEndpoints[0].pbft.viewChangePeriod = 0
	net.pbftEndpoints[0].pbft.viewChangeSeqNo = ^uint64(0)

	for n := 1; n < 3; n++ {
		for _, pe := range net.pbftEndpoints {
			pe.manager.Queue() <- createPbftReqBatch(int64(n), 0)
		}
		net.process()
	}

	for _, pep := range net.pbftEndpoints {
		if pep.sc.executions != 2 {
			t.Errorf("Instance %d executed incorrect number of transactions: %d", pep.id, pep.sc.executions)
		}
		if pep.pbft.view != 1 {
			t.Errorf("Instance %d: expected view=1, got view=%d", pep.id, pep.pbft.view)
		}
	}
}

/*
// TestViewChangeCannotExecuteToCheckpoint tests a replica mid-execution, which receives a view change to a checkpoint above its watermarks
// but does _not_ have enough commit certificates to reach the checkpoint. state should transfer
func TestViewChangeCannotExecuteToCheckpoint(t *testing.T) {
	instance := newPbftCore(3, loadConfig(), &omniProto{
		broadcastImpl:       func(b []byte) {},
		getStateImpl:        func() []byte { return []byte("state") },
		signImpl:            func(b []byte) ([]byte, error) { return b, nil },
		verifyImpl:          func(senderID uint64, signature []byte, message []byte) error { return nil },
		invalidateStateImpl: func() {},
	}, &inertTimerFactory{})
	instance.activeView = false
	instance.view = 1
	newViewBaseSeqNo := uint64(10)
	nextExec := uint64(6)
	instance.currentExec = &nextExec

	for i := instance.lastExec; i < newViewBaseSeqNo; i++ {
		commit := &Commit{View: 0, SequenceNumber: i}
		prepare := &Prepare{View: 0, SequenceNumber: i}
		instance.certStore[msgID{v: 0, n: i}] = &msgCert{
			digest:     "", // null request
			prePrepare: &PrePrepare{View: 0, SequenceNumber: i},
			prepare:    []*Prepare{prepare, prepare, prepare},
			commit:     []*Commit{commit, commit, commit},
		}
	}

	vset := make([]*ViewChange, 3)

	cset := []*ViewChange_C{
		{
			SequenceNumber: newViewBaseSeqNo,
			Id:             base64.StdEncoding.EncodeToString([]byte("Ten")),
		},
	}

	for i := 0; i < 3; i++ {
		// Replica 0 sent checkpoints for 100
		vset[i] = &ViewChange{
			H:    newViewBaseSeqNo,
			Cset: cset,
		}
	}

	xset := make(map[uint64]string)
	xset[11] = ""

	instance.lastExec = 9

	instance.newViewStore[1] = &NewView{
		View:      1,
		Vset:      vset,
		Xset:      xset,
		ReplicaId: 1,
	}

	if _, ok := instance.processNewView().(viewChangedEvent); !ok {
		t.Fatalf("Should have processed the new view")
	}

	if !instance.skipInProgress {
		t.Fatalf("Should have done state transfer")
	}
}

// TestViewChangeCanExecuteToCheckpoint tests a replica mid-execution, which receives a view change to a checkpoint above its watermarks
// but which has enough commit certificates to reach the checkpoint. State should not transfer and executions should trigger the view change
func TestViewChangeCanExecuteToCheckpoint(t *testing.T) {
	instance := newPbftCore(3, loadConfig(), &omniProto{
		broadcastImpl: func(b []byte) {},
		getStateImpl:  func() []byte { return []byte("state") },
		signImpl:      func(b []byte) ([]byte, error) { return b, nil },
		verifyImpl:    func(senderID uint64, signature []byte, message []byte) error { return nil },
		skipToImpl: func(s uint64, id []byte, replicas []uint64) {
			t.Fatalf("Should not have performed state transfer, should have caught up via execution")
		},
	}, &inertTimerFactory{})
	instance.activeView = false
	instance.view = 1
	instance.lastExec = 5
	newViewBaseSeqNo := uint64(10)
	nextExec := uint64(6)
	instance.currentExec = &nextExec

	for i := nextExec + 1; i <= newViewBaseSeqNo; i++ {
		commit := &Commit{View: 0, SequenceNumber: i}
		prepare := &Prepare{View: 0, SequenceNumber: i}
		instance.certStore[msgID{v: 0, n: i}] = &msgCert{
			digest:     "", // null request
			prePrepare: &PrePrepare{View: 0, SequenceNumber: i},
			prepare:    []*Prepare{prepare, prepare, prepare},
			commit:     []*Commit{commit, commit, commit},
		}
	}

	vset := make([]*ViewChange, 3)

	cset := []*ViewChange_C{
		{
			SequenceNumber: newViewBaseSeqNo,
			Id:             base64.StdEncoding.EncodeToString([]byte("Ten")),
		},
	}

	for i := 0; i < 3; i++ {
		// Replica 0 sent checkpoints for 100
		vset[i] = &ViewChange{
			H:    newViewBaseSeqNo,
			Cset: cset,
		}
	}

	xset := make(map[uint64]string)
	xset[11] = ""

	instance.lastExec = 9

	instance.newViewStore[1] = &NewView{
		View:      1,
		Vset:      vset,
		Xset:      xset,
		ReplicaId: 1,
	}

	if instance.processNewView() != nil {
		t.Fatalf("Should not have processed the new view")
	}

	events.SendEvent(instance, execDoneEvent{})

	if !instance.activeView {
		t.Fatalf("Should have finished processing new view after executions")
	}
}

func TestViewWithOldSeqNos(t *testing.T) {
	instance := newPbftCore(3, loadConfig(), &omniProto{
		broadcastImpl: func(b []byte) {},
		signImpl:      func(b []byte) ([]byte, error) { return b, nil },
		verifyImpl:    func(senderID uint64, signature []byte, message []byte) error { return nil },
	}, &inertTimerFactory{})
	instance.activeView = false
	instance.view = 1

	vset := make([]*ViewChange, 3)

	cset := []*ViewChange_C{
		{
			SequenceNumber: 0,
			Id:             base64.StdEncoding.EncodeToString([]byte("Zero")),
		},
	}

	qset := []*ViewChange_PQ{
		{
			SequenceNumber: 9,
			BatchDigest:    "nine",
			View:           0,
		},
		{
			SequenceNumber: 2,
			BatchDigest:    "two",
			View:           0,
		},
	}

	for i := 0; i < 3; i++ {
		// Replica 0 sent checkpoints for 100
		vset[i] = &ViewChange{
			H:    0,
			Cset: cset,
			Qset: qset,
			Pset: qset,
		}
	}

	xset := instance.assignSequenceNumbers(vset, 0)

	instance.lastExec = 10
	instance.moveWatermarks(instance.lastExec)

	instance.newViewStore[1] = &NewView{
		View:      1,
		Vset:      vset,
		Xset:      xset,
		ReplicaId: 1,
	}

	if _, ok := instance.processNewView().(viewChangedEvent); !ok {
		t.Fatalf("Failed to successfully process new view")
	}

	for idx, val := range instance.certStore {
		if idx.n < instance.h {
			t.Errorf("Found %+v=%+v in certStore who's seqNo < %d", idx, val, instance.h)
		}
	}
}

func TestViewChangeDuringExecution(t *testing.T) {
	skipped := false
	instance := newPbftCore(3, loadConfig(), &omniProto{
		viewChangeImpl: func(v uint64) {},
		skipToImpl: func(s uint64, id []byte, replicas []uint64) {
			skipped = true
		},
		invalidateStateImpl: func() {},
		broadcastImpl:       func(b []byte) {},
		signImpl:            func(b []byte) ([]byte, error) { return b, nil },
		verifyImpl:          func(senderID uint64, signature []byte, message []byte) error { return nil },
	}, &inertTimerFactory{})
	instance.activeView = false
	instance.view = 1
	instance.lastExec = 1
	nextExec := uint64(2)
	instance.currentExec = &nextExec

	vset := make([]*ViewChange, 3)

	cset := []*ViewChange_C{
		{
			SequenceNumber: 100,
			Id:             base64.StdEncoding.EncodeToString([]byte("onehundred")),
		},
	}

	// Replica 0 sent checkpoints for 100
	vset[0] = &ViewChange{
		H:    90,
		Cset: cset,
	}

	// Replica 1 sent checkpoints for 10
	vset[1] = &ViewChange{
		H:    90,
		Cset: cset,
	}

	// Replica 2 sent checkpoints for 10
	vset[2] = &ViewChange{
		H:    90,
		Cset: cset,
	}

	xset := make(map[uint64]string)
	xset[101] = ""

	instance.newViewStore[1] = &NewView{
		View:      1,
		Vset:      vset,
		Xset:      xset,
		ReplicaId: 1,
	}

	if _, ok := instance.processNewView().(viewChangedEvent); !ok {
		t.Fatalf("Failed to successfully process new view")
	}

	if skipped {
		t.Fatalf("Expected state transfer not to be kicked off until execution completes")
	}

	events.SendEvent(instance, execDoneEvent{})

	if !skipped {
		t.Fatalf("Expected state transfer to be kicked off once execution completed")
	}
}
*/

func TestStateTransferCheckpoint(t *testing.T) {
	broadcasts := 0
	instance := newPbftCore(3, loadConfig(), &omniProto{
		broadcastImpl: func(msg []byte) {
			broadcasts++
		},
		validateStateImpl: func() {},
	}, &inertTimerFactory{})

	id := []byte("My ID")
	events.SendEvent(instance, stateUpdatedEvent{
		chkpt: &checkpointMessage{
			seqNo: 10,
			id:    id,
		},
		target: &pb.BlockchainInfo{},
	})

	if broadcasts != 1 {
		t.Fatalf("Should have broadcast a checkpoint after the state transfer finished")
	}

}

func TestStateTransferredToOldPoint(t *testing.T) {
	skipped := false
	instance := newPbftCore(3, loadConfig(), &omniProto{
		skipToImpl: func(s uint64, id []byte, replicas []uint64) {
			skipped = true
		},
		invalidateStateImpl: func() {},
	}, &inertTimerFactory{})
	instance.moveWatermarks(90)
	instance.updateHighStateTarget(&stateUpdateTarget{
		checkpointMessage: checkpointMessage{
			seqNo: 100,
			id:    []byte("onehundred"),
		},
	})

	events.SendEvent(instance, stateUpdatedEvent{
		chkpt: &checkpointMessage{
			seqNo: 10,
		},
	})

	if !skipped {
		t.Fatalf("Expected state transfer to be kicked off once execution completed")
	}
}

func TestStateNetworkMovesOnDuringSlowStateTransfer(t *testing.T) {
	instance := newPbftCore(3, loadConfig(), &omniProto{
		skipToImpl:          func(s uint64, id []byte, replicas []uint64) {},
		invalidateStateImpl: func() {},
	}, &inertTimerFactory{})
	instance.skipInProgress = true

	seqNo := uint64(20)

	for i := uint64(0); i < 3; i++ {
		events.SendEvent(instance, &Checkpoint{
			SequenceNumber: seqNo,
			ReplicaId:      i,
			Id:             base64.StdEncoding.EncodeToString([]byte("twenty")),
		})
	}

	if instance.h != seqNo {
		t.Fatalf("Expected watermark movement to %d because of state transfer, but low watermark is %d", seqNo, instance.h)
	}
}

// This test is designed to ensure the peer panics if the value of the weak cert is different from its own checkpoint
func TestCheckpointDiffersFromWeakCert(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Weak checkpoint certificate different from own, should have panicked.")
		}
	}()

	instance := newPbftCore(3, loadConfig(), &omniProto{}, &inertTimerFactory{})

	badChkpt := &Checkpoint{
		SequenceNumber: 10,
		Id:             "WRONG",
		ReplicaId:      3,
	}
	instance.chkpts[10] = badChkpt.Id // This is done via the exec path, shortcut it here
	events.SendEvent(instance, badChkpt)

	for i := uint64(0); i < 2; i++ {
		events.SendEvent(instance, &Checkpoint{
			SequenceNumber: 10,
			Id:             "CORRECT",
			ReplicaId:      i,
		})
	}

	if instance.highStateTarget != nil {
		t.Fatalf("State target should not have been updated")
	}
}

// This test is designed to ensure the peer panics if it observes > f+1 different checkpoint values for the same seqNo
// This indicates a network that will be unable to move its watermarks and thus progress
func TestNoCheckpointQuorum(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("More than f+1 different checkpoint values found, should have panicked.")
		}
	}()

	instance := newPbftCore(3, loadConfig(), &omniProto{}, &inertTimerFactory{})

	for i := uint64(0); i < 3; i++ {
		events.SendEvent(instance, &Checkpoint{
			SequenceNumber: 10,
			Id:             strconv.FormatUint(i, 10),
			ReplicaId:      i,
		})
	}
}
