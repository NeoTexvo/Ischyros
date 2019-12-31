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
	"encoding/base64"
	"fmt"
	"reflect"
  "strconv"
	"github.com/hyperledger/fabric/consensus/util/events"
)

// viewChangeQuorumEvent is returned to the event loop when a new ViewChange message is received which is part of a quorum cert
type viewChangeQuorumEvent struct{}

type wantViewChangeQuorumEvent struct{}

func (instance *pbftCore) correctViewChange(vc *ViewChange) bool {
	for _, p := range append(vc.Pset, vc.Qset...) {
		if !(p.View < vc.View && p.SeqNo > vc.H && p.SeqNo <= vc.H+instance.L) {
			logger.Debugf("Replica %d invalid p entry in view-change: vc(v:%d h:%d) p(v:%d n:%d)",
				instance.id, vc.View, vc.H, p.View, p.SeqNo)
			return false
		}

    // verify prepare and commit attestation
    for _, att := range p.Prepare {
      if ok, _:= instance.a2m.VerifyAttestation(att.Attestation); ok==0 {
        logger.Infof("Replica %d failed to verify attestation for p.Prepare during view change from %d to %d", instance.id, vc.OldView, vc.View)
        return false
      }
    }
    for _, att := range p.Commit {
      if ok, _:= instance.a2m.VerifyAttestation(att.Attestation); ok==0 {
        logger.Infof("Replica %d failed to verify attestation for p.Commit during view change from %d to %d", instance.id, vc.OldView, vc.View)
        return false
      }
    }

    // verify H and A
    checkH, _ := instance.a2m.VerifyAttestation(p.H)
    checkA, _ := instance.a2m.VerifyAttestation(p.A)
    if checkH==0 || checkA==0 {
      logger.Infof("Replica %d failed to verify attestation for H and A during view change from %d to %d", instance.id, vc.OldView, vc.View)
      return false
    }
	}

  // check that any seqNo in Q but not in P
  pSeqs := make(map[uint64]bool)
  for _, p := range vc.Pset {
    pSeqs[p.SeqNo] = true
  }
  
  for _, q := range vc.Qset {
    if _, ok := pSeqs[q.SeqNo]; !ok {
      logger.Infof("Replica %d failed to verify Pset and Qset overlapping, during view change from %d to %d", instance.id, vc.OldView, vc.View)
      return false
    }
  }

	for _, c := range vc.Cset {
		// PBFT: the paper says c.n > vc.h
		if !(c.Checkpoint.SequenceNumber >= vc.H && c.Checkpoint.SequenceNumber <= vc.H+instance.L) {
			logger.Debugf("Replica %d invalid c entry in view-change: vc(v:%d h:%d) c(n:%d)",
				instance.id, vc.View, vc.H, c.Checkpoint.SequenceNumber)
			return false
		}
	}

  // verify B set
  for _, b := range vc.Bset {
    if !(b.View >= vc.OldView && b.View <= vc.View) {
      logger.Infof("Replica %d failed to verify B set: b.View %d not from the expected range [%d, %d]",   instance.id, b.View, vc.OldView, vc.View)
      return false
    }
    if ok, _ := instance.a2m.VerifySkippedAttestation(b.Attestation); ok==0 {
      logger.Infof("Replica %d failed to verify attestation for B set during view change from %d to %d", instance.id, vc.OldView, vc.View)
      return false
    }
  }

	return true
}

func (instance *pbftCore) calcBSet() error {
  // find max seqNo in pset and qset
  max := uint64(0)
  for idx := range instance.pset {
    if max > idx {
      max = idx
    }
  }
  for idx := range instance.qset {
    if max > idx {
      max = idx
    }
  }

  for v := range instance.bset {
    cert := instance.bset[v]
    cert.Attestation, _ = instance.a2m.Lookup("commit", int(v*instance.a2mL+max+1))
  }
 return nil
}

// update P.A
func (instance *pbftCore) updatePSet(seqNo uint64, attestation []byte) {
  if p, ok := instance.pset[seqNo]; !ok {
    return
  } else {
    p.A = attestation
  }
}

// update Q.H
func (instance *pbftCore) updateQSet(seqNo uint64, attestation []byte) {
  if q, ok := instance.qset[seqNo]; !ok {
    return
  } else {
    q.H = attestation
  }
}

func (instance *pbftCore) calcPSet() map[uint64]*ViewChange_PQ {
	pset := make(map[uint64]*ViewChange_PQ)

	for n, p := range instance.pset {
		pset[n] = p
	}

	// P set: requests that have prepared here
	//
	// "<n,d,v> has a prepared certificate, and no request
	// prepared in a later view with the same number"

	for idx, cert := range instance.certStore {
		if cert.prePrepare == nil {
			continue
		}

		digest := cert.digest
		if !instance.prepared(digest, idx.v, idx.n) {
			continue
		}

		if p, ok := pset[idx.n]; ok && p.View > idx.v {
			continue
		}

    var prepareCerts []*Prepare
    for _, p := range cert.prepare {
      if p.View == idx.v && p.SequenceNumber == idx.n {
        prepareCerts = append(prepareCerts, p)
      }
    }

		pset[idx.n] = &ViewChange_PQ{
      SeqNo:  idx.n,
			Prepare: prepareCerts,
      BatchDigest: digest,
      View: idx.v,
		}
	}

	return pset
}

func (instance *pbftCore) calcQSet() map[uint64]*ViewChange_PQ {
	qset := make(map[uint64]*ViewChange_PQ)

	for n, q := range instance.qset {
		qset[n] = q
	}

	// Q set: committed certificates
	//
	// "<n,d,v>: requests that pre-prepared here, and did not
	// pre-prepare in a later view with the same number"

	for idx, cert := range instance.certStore {
		if cert.prePrepare == nil {
			continue
		}

		digest := cert.digest
		if !instance.committed(digest, idx.v, idx.n) {
			continue
		}

		if q, ok := qset[idx.n]; ok && q.View > idx.v {
			continue
		}

    var commitCerts []*Commit
    for _, c := range cert.commit {
      if c.View == idx.v && c.SequenceNumber == idx.n {
        commitCerts = append(commitCerts, c)
      }
    }

    // have a q
		qset[idx.n] = &ViewChange_PQ{
      SeqNo:  idx.n, 
			Commit: commitCerts,
      BatchDigest: digest,
      View: idx.v,
		}
	}

	return qset
}

func (instance *pbftCore) sendWantViewChange() events.Event {
	wvc := &WantViewChange{
		View:      instance.view+1,
		Nonce:     make([]byte, 32),
		Id:        instance.id,
	}

  // no need to sign
	// instance.sign(vc)

	logger.Infof("Replica %d sending want-view-change, v:%d", instance.id, wvc.View)

	instance.innerBroadcast(&Message{Payload: &Message_WantViewChange{WantViewChange: wvc}})

	instance.wvcResendTimer.Reset(instance.wvcResendTimeout, wantViewChangeResendTimerEvent{})

	return instance.recvWantViewChange(wvc)
}

func (instance *pbftCore) sendViewChange() events.Event {
	instance.stopTimer()
  if _, ok := instance.statUtil.Stats["viewchange"]; !ok {
    instance.statUtil.Stats["viewchange"].Start(strconv.FormatUint(instance.id, 10))
  }

	delete(instance.newViewStore, instance.view)
  if instance.activeView {
    instance.oldView = instance.view  // first view change
  }

	instance.view++
	instance.activeView = false

	instance.pset = instance.calcPSet()
	instance.qset = instance.calcQSet()
  instance.calcBSet()

	// clear old messages
	for idx := range instance.certStore {
		if idx.v < instance.view {
			delete(instance.certStore, idx)
		}
	}
	for idx := range instance.viewChangeStore {
		if idx.v < instance.view {
			delete(instance.viewChangeStore, idx)
		}
	}

	vc := &ViewChange{
		View:      instance.view,
    OldView:  instance.oldView,
		H:         instance.h,
		ReplicaId: instance.id,
	}

  // Wset
  // delete right afterward is safe
  for idx := range instance.wset {
    vc.Wset = append(vc.Wset, idx)
    delete(instance.wset, idx)
  }

  // Bset
  for idx := range instance.bset {
    vc.Bset = append(vc.Bset, instance.bset[idx])
  }

  // checkpoint set
  for testChkpt := range instance.checkpointStore {
    if testChkpt.SequenceNumber == instance.h {
      vc.Cset = append(vc.Cset, &ViewChange_C {
                                  Checkpoint: &testChkpt,
                                  })
    }
  }

	for _, p := range instance.pset {
		if p.SeqNo < instance.h {
			logger.Errorf("BUG! Replica %d should not have anything in our pset less than h, found %+v", instance.id, p)
		}
		vc.Pset = append(vc.Pset, p)
	}

	for _, q := range instance.qset {
		if q.SeqNo < instance.h {
			logger.Errorf("BUG! Replica %d should not have anything in our qset less than h, found %+v", instance.id, q)
		}
		vc.Qset = append(vc.Qset, q)
	}

	instance.sign(vc)

	logger.Infof("Replica %d sending view-change, old_view: %d, v:%d, h:%d, |C|:%d, |P|:%d, |Q|:%d",
		instance.id, vc.OldView, vc.View, vc.H, len(vc.Cset), len(vc.Pset), len(vc.Qset))

  // log to a2m
  att, _ := instance.a2m.Advance("viewchange", int(vc.View*instance.a2mL + vc.H), make([]byte, 32), []byte("0"))
  vc.Attestation = att

	instance.innerBroadcast(&Message{Payload: &Message_ViewChange{ViewChange: vc}})

	instance.vcResendTimer.Reset(instance.vcResendTimeout, viewChangeResendTimerEvent{})

 	return instance.recvViewChange(vc)
}

func (instance *pbftCore) recvWantViewChange(wvc *WantViewChange) events.Event {
  logger.Infof("Replica %d received Want-View-Change from replica %d for view %d", instance.id, wvc.Id,
  wvc.View)

  // ignore if wanted view change is smaller than current view
  if wvc.View < instance.view {
    return nil
  }

  // delete old want-view-change from the replica
  if v, ok := instance.wvcIndex[wvc.Id]; ok && v < wvc.View {
    delete(instance.wantVCStore, vcidx{wvc.View, wvc.Id})
  }

  // add to the want-view-change store
  instance.wantVCStore[vcidx{wvc.View, wvc.Id}] = wvc
  instance.wvcIndex[wvc.Id] = wvc.View

  // pick a view that has >= f+1 want-view-change messages
  replicas := make(map[uint64][]bool)
  view2Change := uint64(0)
  for idx := range instance.wantVCStore {
    if idx.v <= instance.view {
      continue
    }
    replicas[idx.v] = append(replicas[idx.v], true)
    if len(replicas[idx.v]) >= (instance.f) {
      view2Change = idx.v
      break
    }
  }

  // got a quorum of Want-View-Change
  if view2Change >0 {

    // make wset
    for idx := range instance.wantVCStore {
      if idx.v == view2Change {
        instance.wset[instance.wantVCStore[idx]] = true
      }
    }

    if len(instance.wset) < instance.intersectionQuorum()-1 {
      return nil
    }

    // abandon from current view to view2Change
    // add to Bset
    for vt := instance.view; vt < view2Change; vt++ {
      abandon_att, _ := instance.a2m.Advance("commit", int((vt+1)*instance.a2mL-1), make([]byte, 32), []byte("0"))
      instance.bset[vt] = &ViewChange_Att{View: vt, Attestation: abandon_att}
    }
    // stop resend timer
    instance.wvcResendTimer.Stop()
    instance.view = view2Change - 1 // because sendViewChange increments it
    instance.sendViewChange()
  }
  return nil
}

func (instance *pbftCore) recvViewChange(vc *ViewChange) events.Event {
	logger.Infof("Replica %d received view-change from replica %d, v:%d, h:%d, |C|:%d, |P|:%d, |Q|:%d",
		instance.id, vc.ReplicaId, vc.View, vc.H, len(vc.Cset), len(vc.Pset), len(vc.Qset))
  if _, ok := instance.statUtil.Stats["viewchange"]; !ok {
    instance.statUtil.Stats["viewchange"].Start(strconv.FormatUint(instance.id, 10))
  }

	if err := instance.verify(vc); err != nil {
		logger.Warningf("Replica %d found incorrect signature in view-change message: %s", instance.id, err)
		return nil
	}

	if vc.View < instance.view {
		logger.Warningf("Replica %d found view-change message for old view", instance.id)
		return nil
	}

	if !instance.correctViewChange(vc) {
		logger.Warningf("Replica %d found view-change message incorrect", instance.id)
		return nil
	}

	if _, ok := instance.viewChangeStore[vcidx{vc.View, vc.ReplicaId}]; ok {
		logger.Warningf("Replica %d already has a view change message for view %d from replica %d", instance.id, vc.View, vc.ReplicaId)
		return nil
	}

	instance.viewChangeStore[vcidx{vc.View, vc.ReplicaId}] = vc

	// PBFT TOCS 4.5.1 Liveness: "if a replica receives a set of
	// f+1 valid VIEW-CHANGE messages from other replicas for
	// views greater than its current view, it sends a VIEW-CHANGE
	// message for the smallest view in the set, even if its timer
	// has not expired"
	replicas := make(map[uint64]bool)
	minView := uint64(0)
	for idx := range instance.viewChangeStore {
		if idx.v <= instance.view {
			continue
		}

		replicas[idx.id] = true
		if minView == 0 || idx.v < minView {
			minView = idx.v
		}
	}

	// We only enter this if there are enough view change messages _greater_ than our current view
	if  len(replicas) >= instance.f {
		logger.Infof("Replica %d received f+1 view-change messages, triggering view-change to view %d",
			instance.id, minView)
		// subtract one, because sendViewChange() increments
		instance.view = minView - 1
		return instance.sendViewChange()
	}

	quorum := 0
	for idx := range instance.viewChangeStore {
		if idx.v == instance.view {
			quorum++
		}
	}
	logger.Debugf("Replica %d now has %d view change requests for view %d", instance.id, quorum, instance.view)

	if !instance.activeView && vc.View == instance.view && quorum >= instance.allCorrectReplicasQuorum() {
		instance.vcResendTimer.Stop()
		instance.startTimer(instance.lastNewViewTimeout, "new view change")
		instance.lastNewViewTimeout = 2 * instance.lastNewViewTimeout
		return viewChangeQuorumEvent{}
	}

	return nil
}

func (instance *pbftCore) sendNewView() events.Event {

	if _, ok := instance.newViewStore[instance.view]; ok {
		logger.Debugf("Replica %d already has new view in store for view %d, skipping", instance.id, instance.view)
		return nil
	}

	vset := instance.getViewChanges()

	cp, ok, _ := instance.selectInitialCheckpoint(vset)
	if !ok {
		logger.Infof("Replica %d could not find consistent checkpoint: %+v", instance.id, instance.viewChangeStore)
		return nil
	}

	msgList := instance.assignSequenceNumbers(vset, cp.Checkpoint.SequenceNumber)
	if msgList == nil {
		logger.Infof("Replica %d could not assign sequence numbers for new view", instance.id)
		return nil
	}

	nv := &NewView{
		View:      instance.view,
		Vset:      vset,
		Xset:      msgList,
		ReplicaId: instance.id,
	}

  att, _ := instance.a2m.Advance("newview", int(instance.view*instance.a2mL), make([]byte, 32), []byte("0"))
  nv.Attestation = att

	logger.Infof("Replica %d is new primary, sending new-view, v:%d, X:%+v",
		instance.id, nv.View, nv.Xset)

	instance.innerBroadcast(&Message{Payload: &Message_NewView{NewView: nv}})
	instance.newViewStore[instance.view] = nv
	return instance.processNewView()
}

func (instance *pbftCore) recvNewView(nv *NewView) events.Event {
	logger.Infof("Replica %d received new-view %d",
		instance.id, nv.View)

	if !(nv.View > 0 && nv.View >= instance.view && instance.primary(nv.View) == nv.ReplicaId && instance.newViewStore[nv.View] == nil) {
		logger.Infof("Replica %d rejecting invalid new-view from %d, v:%d",
			instance.id, nv.ReplicaId, nv.View)
		return nil
	}

	for _, vc := range nv.Vset {
		if err := instance.verify(vc); err != nil {
			logger.Warningf("Replica %d found incorrect view-change signature in new-view message: %s", instance.id, err)
			return nil
		}
	}

	instance.newViewStore[nv.View] = nv
	return instance.processNewView()
}

func (instance *pbftCore) processNewView() events.Event {
	var newReqBatchMissing bool
	nv, ok := instance.newViewStore[instance.view]
	if !ok {
		logger.Debugf("Replica %d ignoring processNewView as it could not find view %d in its newViewStore", instance.id, instance.view)
		return nil
	}

	if instance.activeView {
		logger.Infof("Replica %d ignoring new-view from %d, v:%d: we are active in view %d",
			instance.id, nv.ReplicaId, nv.View, instance.view)
		return nil
	}

	cp, ok, replicas := instance.selectInitialCheckpoint(nv.Vset)
	if !ok {
		logger.Warningf("Replica %d could not determine initial checkpoint: %+v",
			instance.id, instance.viewChangeStore)
		return instance.sendViewChange()
	}

	speculativeLastExec := instance.lastExec
	if instance.currentExec != nil {
		speculativeLastExec = *instance.currentExec
	}

	// If we have not reached the sequence number, check to see if we can reach it without state transfer
	// In general, executions are better than state transfer
	if speculativeLastExec < cp.Checkpoint.SequenceNumber {
		canExecuteToTarget := true
	outer:
		for seqNo := speculativeLastExec + 1; seqNo <= cp.Checkpoint.SequenceNumber; seqNo++ {
			found := false
			for idx, cert := range instance.certStore {
				if idx.n != seqNo {
					continue
				}

				quorum := 0
				for _, p := range cert.commit {
					// Was this committed in the previous view
					if p.View == idx.v && p.SequenceNumber == seqNo {
						quorum++
					}
				}

				if quorum < instance.intersectionQuorum() {
					logger.Debugf("Replica %d missing quorum of commit certificate for seqNo=%d, only has %d of %d", instance.id, quorum, instance.intersectionQuorum())
					continue
				}

				found = true
				break
			}

			if !found {
				canExecuteToTarget = false
				logger.Debugf("Replica %d missing commit certificate for seqNo=%d", instance.id, seqNo)
				break outer
			}

		}

		if canExecuteToTarget {
			logger.Debugf("Replica %d needs to process a new view, but can execute to the checkpoint seqNo %d, delaying processing of new view", instance.id, cp.Checkpoint.SequenceNumber)
			return nil
		}

		logger.Infof("Replica %d cannot execute to the view change checkpoint with seqNo %d", instance.id,  cp.Checkpoint.SequenceNumber)
	}

	msgList := instance.assignSequenceNumbers(nv.Vset, cp.Checkpoint.SequenceNumber)
	if msgList == nil {
		logger.Warningf("Replica %d could not assign sequence numbers: %+v",
			instance.id, instance.viewChangeStore)
		return instance.sendViewChange()
	}

	if !(len(msgList) == 0 && len(nv.Xset) == 0) && !reflect.DeepEqual(msgList, nv.Xset) {
		logger.Warningf("Replica %d failed to verify new-view Xset: computed %+v, received %+v",
			instance.id, msgList, nv.Xset)
		return instance.sendViewChange()
	}

	if instance.h < cp.Checkpoint.SequenceNumber {
		instance.moveWatermarks(cp.Checkpoint.SequenceNumber)
	}

	if speculativeLastExec < cp.Checkpoint.SequenceNumber {
		logger.Warningf("Replica %d missing base checkpoint %d (%s), our most recent execution %d", instance.id,  cp.Checkpoint.SequenceNumber, cp.Checkpoint.Id, speculativeLastExec)

		snapshotID, err := base64.StdEncoding.DecodeString(cp.Checkpoint.Id)
		if nil != err {
			err = fmt.Errorf("Replica %d received a view change whose hash could not be decoded (%s)", instance.id, cp.Checkpoint.Id)
			logger.Error(err.Error())
			return nil
		}

		target := &stateUpdateTarget{
			checkpointMessage: checkpointMessage{
				seqNo: cp.Checkpoint.SequenceNumber,
				id:    snapshotID,
			},
			replicas: replicas,
		}

		instance.updateHighStateTarget(target)
		instance.stateTransfer(target)
	}

	for n, d := range nv.Xset {
		// PBFT: why should we use "h ≥ min{n | ∃d : (<n,d> ∈ X)}"?
		// "h ≥ min{n | ∃d : (<n,d> ∈ X)} ∧ ∀<n,d> ∈ X : (n ≤ h ∨ ∃m ∈ in : (D(m) = d))"
		if n <= instance.h {
			continue
		} else {
			if d == "" {
				// NULL request; skip
				continue
			}

			if _, ok := instance.reqBatchStore[d]; !ok {
				logger.Warningf("Replica %d missing assigned, non-checkpointed request batch %s",
					instance.id, d)
				if _, ok := instance.missingReqBatches[d]; !ok {
					logger.Warningf("Replica %v requesting to fetch batch %s",
						instance.id, d)
					newReqBatchMissing = true
					instance.missingReqBatches[d] = true
				}
			}
		}
	}

	if len(instance.missingReqBatches) == 0 {
		return instance.processNewView2(nv)
	} else if newReqBatchMissing {
		instance.fetchRequestBatches()
	}

	return nil
}

func (instance *pbftCore) processNewView2(nv *NewView) events.Event {
	logger.Infof("Replica %d accepting new-view to view %d", instance.id, instance.view)

	instance.stopTimer()
	instance.nullRequestTimer.Stop()

	instance.activeView = true
  // clear Bset
  for bidx := range instance.bset {
    delete(instance.bset, bidx)
  }

	delete(instance.newViewStore, instance.view-1)

	instance.seqNo = instance.h
	for n, d := range nv.Xset {
		if n <= instance.h {
			continue
		}

		reqBatch, ok := instance.reqBatchStore[d]
		if !ok && d != "" {
			logger.Criticalf("Replica %d is missing request batch for seqNo=%d with digest '%s' for assigned prepare after fetching, this indicates a serious bug", instance.id, n, d)
		}
		preprep := &PrePrepare{
			View:           instance.view,
			SequenceNumber: n,
			BatchDigest:    d,
			RequestBatch:   reqBatch,
			ReplicaId:      instance.id,
		}
    att, _ := instance.a2m.Advance("prep", int(instance.view*instance.a2mL+n), make([]byte, 32), []byte("0"))
    preprep.Attestation = att

		cert := instance.getCert(instance.view, n)
		cert.prePrepare = preprep
		cert.digest = d
		if n > instance.seqNo {
			instance.seqNo = n
		}
	}

	instance.updateViewChangeSeqNo()

	if instance.primary(instance.view) != instance.id {
		for n, d := range nv.Xset {
			prep := &Prepare{
				View:           instance.view,
				SequenceNumber: n,
				BatchDigest:    d,
				ReplicaId:      instance.id,
			}
      att, _ := instance.a2m.Advance("prepare", int(instance.view*instance.a2mL+n), make([]byte, 32), []byte("0"))
      prep.Attestation = att
			if n > instance.h {
				cert := instance.getCert(instance.view, n)
				cert.sentPrepare = true
				instance.recvPrepare(prep)
			}
			instance.innerBroadcast(&Message{Payload: &Message_Prepare{Prepare: prep}})
		}
	} else {
		logger.Infof("Replica %d is now primary, attempting to resubmit requests", instance.id)
		instance.resubmitRequestBatches()
	}

	instance.startTimerIfOutstandingRequests()

	logger.Infof("Replica %d done cleaning view change artifacts, calling into consumer", instance.id)
  if lt, ok := instance.statUtil.Stats["viewchange"].End(strconv.FormatUint(instance.id, 10)); ok {
      logger.Infof("Viewchange latency (before viewChangedEvent): %v", lt)
  } else {
      logger.Infof("Error printing out viewchange latency (before viewChangedEvent)")
  }

	return viewChangedEvent{}
}

func (instance *pbftCore) getViewChanges() (vset []*ViewChange) {
	for _, vc := range instance.viewChangeStore {
		vset = append(vset, vc)
	}

	return
}

// for a2m, select one that has highest seqNo
func (instance *pbftCore) selectInitialCheckpoint(vset []*ViewChange) (checkpoint ViewChange_C, ok bool, replicas []uint64) {
  maxSeq := uint64(0)
  vcIdx := 0
  for idx, vc := range vset {
    for _, c := range vc.Cset {
      if c.Checkpoint.SequenceNumber >= maxSeq {
        checkpoint = *c
        maxSeq = c.Checkpoint.SequenceNumber
        vcIdx = idx
      }
    }
  }
  ok = true
  for _, c := range vset[vcIdx].Cset {
     replicas = append(replicas, c.Checkpoint.ReplicaId)
  }
  return

  /*
	checkpoints := make(map[ViewChange_C][]*ViewChange)
	for _, vc := range vset {
		for _, c := range vc.Cset { // TODO, verify that we strip duplicate checkpoints from this set
			checkpoints[*c] = append(checkpoints[*c], vc)
			logger.Debugf("Replica %d appending checkpoint from replica %d with seqNo=%d, h=%d, and checkpoint digest %s", instance.id, vc.ReplicaId, vc.H, c.SequenceNumber, c.Id)
		}
	}

	if len(checkpoints) == 0 {
		logger.Debugf("Replica %d has no checkpoints to select from: %d %s",
			instance.id, len(instance.viewChangeStore), checkpoints)
		return
	}

	for idx, vcList := range checkpoints {
		// need weak certificate for the checkpoint
		if (!instance.sgx && len(vcList) <= instance.f)  { // type casting necessary to match types
			logger.Debugf("Replica %d has no weak certificate for n:%d, vcList was %d long",
				instance.id, idx.SequenceNumber, len(vcList))
			continue
		}

		quorum := 0
		// Note, this is the whole vset (S) in the paper, not just this checkpoint set (S') (vcList)
		// We need 2f+1 low watermarks from S below this seqNo from all replicas
		// We need f+1 matching checkpoints at this seqNo (S')
		for _, vc := range vset {
			if vc.H <= idx.SequenceNumber {
				quorum++
			}
		}

		if quorum < instance.intersectionQuorum() {
			logger.Debugf("Replica %d has no quorum for n:%d", instance.id, idx.SequenceNumber)
			continue
		}

		replicas = make([]uint64, len(vcList))
		for i, vc := range vcList {
			replicas[i] = vc.ReplicaId
		}

		if checkpoint.SequenceNumber <= idx.SequenceNumber {
			checkpoint = idx
			ok = true
		}
	}

	return
  */
}

// for a2m, get the highest sequence number H in P and Q
// for all h < n <= H, if there's a certificate, add to the list
// if no cert, add Null request to the list
func (instance *pbftCore) assignSequenceNumbers(vset []*ViewChange, h uint64) (msgList map[uint64]string) {
	msgList = make(map[uint64]string)

	maxN := h + 1
  digestMap := make(map[uint64]string)
  for _, v := range vset {
    for _, p := range(append(v.Pset, v.Qset...)) {
      digestMap[p.SeqNo] = p.BatchDigest
        if p.SeqNo > maxN {
          maxN = p.SeqNo
        }
    }
  }

  // populate msgList
  for n := h+1; n <= maxN; n++ {
    msgList[n] = ""
    if d, ok := digestMap[n]; ok {
      msgList[n] = d
    }
  }

  return
/*
	// "for all n such that h < n <= h + L"
nLoop:
	for n := h + 1; n <= h+instance.L; n++ {
		// "∃m ∈ S..."
		for _, m := range vset {
			// "...with <n,d,v> ∈ m.P"
			for _, em := range m.Pset {
				quorum := 0
				// "A1. ∃2f+1 messages m' ∈ S"
			mpLoop:
				for _, mp := range vset {
					if mp.H >= n {
						continue
					}
					// "∀<n,d',v'> ∈ m'.P"
					for _, emp := range mp.Pset {
						if n == emp.SequenceNumber && !(emp.View < em.View || (emp.View == em.View && emp.BatchDigest == em.BatchDigest)) {
							continue mpLoop
						}
					}
					quorum++
				}

				if quorum < instance.intersectionQuorum() {
					continue
				}

				quorum = 0
				// "A2. ∃f+1 messages m' ∈ S"
				for _, mp := range vset {
					// "∃<n,d',v'> ∈ m'.Q"
					for _, emp := range mp.Qset {
						if n == emp.SequenceNumber && emp.View >= em.View && emp.BatchDigest == em.BatchDigest {
							quorum++
						}
					}
				}

				if quorum < instance.f+1 {
					continue
				}

				// "then select the request with digest d for number n"
				msgList[n] = em.BatchDigest
				maxN = n

				continue nLoop
			}
		}

		quorum := 0
		// "else if ∃2f+1 messages m ∈ S"
	nullLoop:
		for _, m := range vset {
			// "m.P has no entry"
			for _, em := range m.Pset {
				if em.SequenceNumber == n {
					continue nullLoop
				}
			}
			quorum++
		}

		if quorum >= instance.intersectionQuorum() {
			// "then select the null request for number n"
			msgList[n] = ""
			continue nLoop
		}

		logger.Warningf("Replica %d could not assign value to contents of seqNo %d, found only %d missing P entries", instance.id, n, quorum)
		return nil
	}

	// prune top null requests
	for n, msg := range msgList {
		if n > maxN && msg == "" {
			delete(msgList, n)
		}
	}

	return
*/
}
