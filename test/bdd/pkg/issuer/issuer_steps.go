/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issuer

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/btcsuite/btcutil/base58"
	"github.com/cucumber/godog"
	docdid "github.com/hyperledger/aries-framework-go/pkg/doc/did"
	log "github.com/sirupsen/logrus"
	"github.com/trustbloc/sidetree-core-go/pkg/docutil"
	"github.com/trustbloc/sidetree-core-go/pkg/restapi/helper"

	"github.com/trustbloc/edge-service/pkg/restapi/vc/operation"
	"github.com/trustbloc/edge-service/test/bdd/pkg/bddutil"
	"github.com/trustbloc/edge-service/test/bdd/pkg/context"
)

const (
	issuerURL   = "http://localhost:8070"
	sidetreeURL = "https://localhost:48326/document"
)

const (
	sha2_256       = 18
	recoveryOTP    = "recoveryOTP"
	updateOTP      = "updateOTP"
	pubKeyIndex1   = "#key-1"
	defaultKeyType = "Ed25519VerificationKey2018"

	validContext = `"@context":["https://www.w3.org/2018/credentials/v1"]`
	validVC      = `{` +
		validContext + `,
	  "id": "http://example.edu/credentials/1872",
	  "type": "VerifiableCredential",
	  "credentialSubject": {
		"id": "did:example:ebfeb1f712ebc6f1c276e12ec21"
	  },
	  "issuer": {
		"id": "did:example:76e12ec712ebc6f1c221ebfeb1f",
		"name": "Example University"
	  },
	  "issuanceDate": "2010-01-01T19:23:24Z",
	  "credentialStatus": {
		"id": "https://example.gov/status/24",
		"type": "CredentialStatusList2017"
	  }
	}`

	composeCredReqFormat = `{
	   "issuer":"did:example:uoweu180928901",
	   "subject":"did:example:oleh394sqwnlk223823ln",
	   "types":[
		  "UniversityDegree"
	   ],
	   "issuanceDate":"2020-03-25T19:38:54.45546Z",
	   "expirationDate":"2020-06-25T19:38:54.45546Z",
	   "claims":{
		  "customField":"customFieldVal",
		  "name":"John Doe"
	   },
	   "evidence":{
		  "customField":"customFieldVal",
		  "id":"http://example.com/policies/credential/4",
		  "type":"IssuerPolicy"
	   },
	   "termsOfUse":{
		  "id":"http://example.com/policies/credential/4",
		  "type":"IssuerPolicy"
	   },
	   "proofFormat":"jws",
	   "proofFormatOptions":{
		  "kid":` + `"%s"` + `
	   }
	}`
)

// Steps is steps for VC BDD tests
type Steps struct {
	bddContext *context.BDDContext
}

// NewSteps returns new agent from client SDK
func NewSteps(ctx *context.BDDContext) *Steps {
	return &Steps{bddContext: ctx}
}

// RegisterSteps registers agent steps
func (e *Steps) RegisterSteps(s *godog.Suite) {
	s.Step(`^Public key stored in "([^"]*)" variable generated by calling Issuer Service Generate Keypair API$`,
		e.generateKeypair)
	s.Step(`^A new DID Document is created using the public key stored in "([^"]*)" and store the generate DID in "([^"]*)" variable$`, //nolint: lll
		e.createDIDDoc)
	s.Step(`^Verify the proof value generated using the Issuer Service Issue Credential API with the DID stored in "([^"]*)" variable$`, //nolint: lll
		e.createAndVerifyCredential)
	s.Step(`^"([^"]*)" has stored her transcript from the University$`, e.createCredential)
	s.Step(`^"([^"]*)" has a DID$`, e.generateDID)
	s.Step(`^"([^"]*)" application service verifies the credential created by Issuer Service issueCredential API with it's DID$`, //nolint: lll
		e.issueCred)
	s.Step(`^"([^"]*)" application service verifies the credential created by Issuer Service composeAndIssueCredential API with it's DID$`, //nolint: lll
		e.composeAndIssueCred)
}

func (e *Steps) generateKeypair(publicKeyVar string) error {
	resp, err := http.Get(issuerURL + "/kms/generatekeypair") //nolint: bodyclose
	if err != nil {
		return err
	}

	defer bddutil.CloseResponseBody(resp.Body)

	respBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return bddutil.ExpectedStatusCodeError(http.StatusOK, resp.StatusCode, respBytes)
	}

	generateKeyPairResponse := operation.GenerateKeyPairResponse{}

	err = json.Unmarshal(respBytes, &generateKeyPairResponse)
	if err != nil {
		return err
	}

	log.Infof("public key %s", generateKeyPairResponse.PublicKey)

	e.bddContext.Args[publicKeyVar] = generateKeyPairResponse.PublicKey

	return nil
}

func (e *Steps) createDIDDoc(publicKeyVar, didDocVar string) error {
	publicKey := e.bddContext.Args[publicKeyVar]

	// create sidetree DID Document
	doc, err := e.createSidetreeDID(publicKey)
	if err != nil {
		return err
	}

	e.bddContext.Args[didDocVar] = doc.ID

	return nil
}

func (e *Steps) generateDID(user string) error {
	publicKeyVar := user + "-publicKeyVar"

	if err := e.generateKeypair(publicKeyVar); err != nil {
		return err
	}

	didVar := user + "-didVarKey"
	if err := e.createDIDDoc(publicKeyVar, didVar); err != nil {
		return err
	}

	e.bddContext.Args[user] = e.bddContext.Args[didVar]

	return nil
}

func (e *Steps) createSidetreeDID(base58PubKey string) (*docdid.Doc, error) {
	req, err := e.buildSideTreeRequest(base58PubKey)
	if err != nil {
		return nil, err
	}

	return e.sendCreateRequest(req)
}

func (e *Steps) createAndVerifyCredential(didVar string) error {
	return e.verifyCredential(e.bddContext.Args[didVar])
}

func (e *Steps) verifyCredential(did string) error {
	signedVCByte, err := e.signCredential(did)
	if err != nil {
		return err
	}

	signedVCResp := make(map[string]interface{})

	err = json.Unmarshal(signedVCByte, &signedVCResp)
	if err != nil {
		return err
	}

	proof, ok := signedVCResp["proof"].(map[string]interface{})
	if !ok {
		return errors.New("unable to convert proof to a map")
	}

	if proof["type"] != "Ed25519Signature2018" {
		return errors.New("proof type is not valid")
	}

	if proof["jws"] == "" {
		return errors.New("proof jws value is empty")
	}

	return nil
}

func (e *Steps) signCredential(did string) ([]byte, error) {
	log.Infof("DID for signing %s", did)

	if err := bddutil.ResolveDID(e.bddContext.VDRI, did, 10); err != nil {
		return nil, err
	}

	req := &operation.IssueCredentialRequest{
		Credential: []byte(validVC),
		Opts:       operation.IssueCredentialOptions{AssertionMethod: did},
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	endpointURL := issuerURL + "/credentials/issueCredential"

	resp, err := http.Post(endpointURL, "application/json", //nolint: bodyclose
		bytes.NewBuffer(reqBytes))
	if err != nil {
		return nil, err
	}

	defer bddutil.CloseResponseBody(resp.Body)

	responseBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response : %s", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("got unexpected response from %s status '%d' body %s",
			endpointURL, resp.StatusCode, responseBytes)
	}

	log.Infof("proof value %s", string(responseBytes))

	return responseBytes, nil
}

func (e *Steps) issueCred(user string) error {
	did := e.bddContext.Args[user]

	return e.verifyCredential(did)
}

func (e *Steps) composeAndIssueCred(user string) error {
	did := e.bddContext.Args[user]
	log.Infof("DID for signing %s", did)

	if err := bddutil.ResolveDID(e.bddContext.VDRI, did, 10); err != nil {
		return err
	}

	req := fmt.Sprintf(composeCredReqFormat, did)

	endpointURL := issuerURL + "/credentials/composeAndIssueCredential"

	resp, err := http.Post(endpointURL, "application/json", //nolint: bodyclose
		bytes.NewBufferString(req))
	if err != nil {
		return err
	}

	defer bddutil.CloseResponseBody(resp.Body)

	responseBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response : %s", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("got unexpected response from %s status '%d' body %s",
			endpointURL, resp.StatusCode, responseBytes)
	}

	log.Infof("vc with proof %s", string(responseBytes))

	signedVCResp := make(map[string]interface{})

	err = json.Unmarshal(responseBytes, &signedVCResp)
	if err != nil {
		return err
	}

	proof, ok := signedVCResp["proof"].(map[string]interface{})
	if !ok {
		return errors.New("unable to convert proof to a map")
	}

	if proof["type"] != "Ed25519Signature2018" {
		return errors.New("proof type is not valid")
	}

	if proof["jws"] == "" {
		return errors.New("proof jws value is empty")
	}

	return nil
}

func (e *Steps) createCredential(user string) error {
	publicKeyVar := user + "-publicKeyVar"

	if err := e.generateKeypair(publicKeyVar); err != nil {
		return err
	}

	didVar := user + "-didVarKey"
	if err := e.createDIDDoc(publicKeyVar, didVar); err != nil {
		return err
	}

	signedVCByte, err := e.signCredential(e.bddContext.Args[didVar])
	if err != nil {
		return err
	}

	e.bddContext.Args[user] = string(signedVCByte)

	return nil
}

func (e *Steps) buildSideTreeRequest(base58PubKey string) ([]byte, error) {
	publicKey := docdid.PublicKey{
		ID:    pubKeyIndex1,
		Type:  defaultKeyType,
		Value: base58.Decode(base58PubKey),
	}

	t := time.Now()

	didDoc := &docdid.Doc{
		Context:   []string{},
		PublicKey: []docdid.PublicKey{publicKey},
		Created:   &t,
		Updated:   &t,
	}

	docBytes, err := didDoc.JSONBytes()
	if err != nil {
		return nil, fmt.Errorf("failed to get document bytes : %s", err)
	}

	req, err := helper.NewCreateRequest(&helper.CreateRequestInfo{
		OpaqueDocument:  string(docBytes),
		RecoveryKey:     "recoveryKey",
		NextRecoveryOTP: docutil.EncodeToString([]byte(recoveryOTP)),
		NextUpdateOTP:   docutil.EncodeToString([]byte(updateOTP)),
		MultihashCode:   sha2_256,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create sidetree request: %w", err)
	}

	return req, nil
}

func (e *Steps) sendCreateRequest(req []byte) (*docdid.Doc, error) {
	client := &http.Client{
		// TODO add tls config https://github.com/trustbloc/edge-service/issues/147
		// TODO !!!!!!!remove InsecureSkipVerify after configure tls for http client
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint: gosec
		}}

	resp, err := client.Post(sidetreeURL, "application/json", bytes.NewBuffer(req)) //nolint: bodyclose
	if err != nil {
		return nil, err
	}

	defer bddutil.CloseResponseBody(resp.Body)

	responseBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response : %s", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("got unexpected response from %s status '%d' body %s",
			sidetreeURL, resp.StatusCode, responseBytes)
	}

	didDoc, err := docdid.ParseDocument(responseBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public DID document: %s", err)
	}

	return didDoc, nil
}
