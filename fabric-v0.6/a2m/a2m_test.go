package a2m

import (
	"testing"
)

func TestInit(t *testing.T) {
	a2m := CreateNewA2M()
  a2m.StartA2M()
}

func TestAppend(t *testing.T) {

  a2m := CreateNewA2M()
  a2m.Append("logName", randomBytes(DIGEST_SIZE))
}

func TestAdvance(t *testing.T) {
  a2m := CreateNewA2M()
	a2m.Advance("logName", 5, randomBytes(DIGEST_SIZE), randomBytes(DIGEST_SIZE))
}

func TestLookup(t *testing.T) {
  a2m := CreateNewA2M()
	a2m.Lookup("logName", 5)

}

func TestVerifyAttestation(t *testing.T) {
  a2m := CreateNewA2M()
	x, _ := a2m.Advance("logName", 5, randomBytes(DIGEST_SIZE), randomBytes(DIGEST_SIZE))
  a2m.VerifyAttestation(x)
  a2m.VerifySkippedAttestation(x)
}

func TestGetMessage(t *testing.T) {
  a2m := CreateNewA2M()
	x, _ := a2m.Advance("logName", 5, randomBytes(DIGEST_SIZE), randomBytes(DIGEST_SIZE))
  a2m.GetMessage(x)
}
