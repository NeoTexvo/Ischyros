package a2m

import "math/rand"

// //go:generate moq -out am2_impl.go . A2MInterface
type A2MInterface interface {
  StartA2M()
  Append(logname string, input []byte) ([]byte, error)
  Advance(logname string, n int, newdigest []byte, input []byte) ([]byte, error)
  Lookup(logname string, n int) ([]byte, error)
  End(logname string) ([]byte, error)
  Truncate(logname string, n int) error
  VerifyMessage(message []byte, attestation []byte) (int, error)
  VerifyAttestation(attestation []byte) (int, error)
  VerifySkippedAttestation(attestation []byte) (int, error)
  GetMessage(attestation []byte) ([]byte, error)
  DestroyA2M()
}

func randomBytes(length int) []byte {
  x := make([]byte, length)
  rand.Read(x)
  return x
}


const MESSAGE_SIZE =121
const DIGEST_SIZE = 32
// create new mock A2M interface
func CreateNewA2M() A2MInterface {
  advanceFunc := func(logname string, n int, newdigest []byte, input []byte) ([]byte, error){
    return randomBytes(MESSAGE_SIZE), nil
  }
  appendFunc := func(logname string, input []byte) ([]byte, error) {
    return randomBytes(MESSAGE_SIZE), nil
  }
  getMessageFunc := func(attestation []byte) ([]byte ,error) {
    return randomBytes(DIGEST_SIZE), nil
  }
  lookupFunc := func(logname string, n int) ([]byte, error) {
    return randomBytes(MESSAGE_SIZE), nil
  }
  startA2MFunc := func() {
  }
  verifyAttestationFunc := func(attestation []byte) (int, error) {
    return 1, nil
  }
  verifyMsgFunc := func(messsage []byte, attestation []byte) (int, error) {
    return 1, nil
  }
  verifySkippedAttestationFunc := func(attestation []byte) (int, error) {
    return 1, nil
  }
  return &A2MInterfaceMock{AdvanceFunc: advanceFunc, AppendFunc: appendFunc, GetMessageFunc: getMessageFunc,
                          LookupFunc: lookupFunc, StartA2MFunc: startA2MFunc,
                          VerifyAttestationFunc: verifyAttestationFunc,
                          VerifySkippedAttestationFunc: verifySkippedAttestationFunc, VerifyMessageFunc: verifyMsgFunc}
}
