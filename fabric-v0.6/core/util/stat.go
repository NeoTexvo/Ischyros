package util

import (
	"sync"
	"time"
  "math/rand"
)

type Stat struct {
  lock sync.Mutex
  stats map[string]time.Time  // index by transaction ID
  sampleInterval uint32 // of the form 2^x - 1
}

// singleton that collect timing metrics
type StatUtil struct {
  Stats map[string]*Stat  // index by stat name
}

func (su *StatUtil) NewStat(name string, sampleInterval uint32) {
  su.Stats[name] = &Stat{}
  su.Stats[name].stats = make(map[string]time.Time)
  su.Stats[name].sampleInterval = sampleInterval
}

func (stat *Stat) Start(id string) {
  if (rand.Uint32() & stat.sampleInterval) == 0 {
    stat.lock.Lock()
    defer stat.lock.Unlock()
    stat.stats[id] = time.Now()
  }
}

// return (time, OK?) where OK = true if
// the id existed (has been Start-ed)
func (stat *Stat) End(id string) (uint64, bool) {
  stat.lock.Lock()
  defer stat.lock.Unlock()
  if val, ok := stat.stats[id]; ok {
    delete(stat.stats, id)
    return uint64(time.Since(val)), ok
  } else {
    return 0, ok
  }
}

var statUtilSyncOnce sync.Once
var statUtil *StatUtil

func GetStatUtil() *StatUtil {
	statUtilSyncOnce.Do(func() {
		statUtil = &StatUtil{}
    statUtil.Stats = make(map[string]*Stat)
	})
	return statUtil
}
