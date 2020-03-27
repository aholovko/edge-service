/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package operation

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/btcsuite/btcutil/base58"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/hyperledger/aries-framework-go/pkg/didcomm/packer/legacy/authcrypt"
	"github.com/hyperledger/aries-framework-go/pkg/doc/did"
	"github.com/hyperledger/aries-framework-go/pkg/doc/signature/ed25519signature2018"
	"github.com/hyperledger/aries-framework-go/pkg/doc/verifiable"
	vdriapi "github.com/hyperledger/aries-framework-go/pkg/framework/aries/api/vdri"
	"github.com/hyperledger/aries-framework-go/pkg/kms/legacykms"
	log "github.com/sirupsen/logrus"
	"github.com/trustbloc/edge-core/pkg/storage"
	"github.com/trustbloc/edv/pkg/restapi/edv/operation"
	didclient "github.com/trustbloc/trustbloc-did-method/pkg/did"

	"github.com/trustbloc/edge-service/pkg/doc/vc/crypto"
	vcprofile "github.com/trustbloc/edge-service/pkg/doc/vc/profile"
	cslstatus "github.com/trustbloc/edge-service/pkg/doc/vc/status/csl"
	"github.com/trustbloc/edge-service/pkg/internal/common/support"
)

const (
	credentialStoreName = "credential"
	profile             = "/profile"
	vcStatus            = "/status"

	// endpoints
	createCredentialEndpoint        = "/credential"
	verifyCredentialEndpoint        = "/verify"
	updateCredentialStatusEndpoint  = "/updateStatus"
	createProfileEndpoint           = profile
	getProfileEndpoint              = profile + "/{id}"
	storeCredentialEndpoint         = "/store"
	retrieveCredentialEndpoint      = "/retrieve"
	verifyPresentationEndpoint      = "/verifyPresentation"
	vcStatusEndpoint                = vcStatus + "/{id}"
	credentialsBasePath             = "/credentials"
	issueCredentialPath             = credentialsBasePath + "/issueCredential"
	composeAndIssueCredentialPath   = credentialsBasePath + "/composeAndIssueCredential"
	kmsBasePath                     = "/kms"
	generateKeypairPath             = kmsBasePath + "/generatekeypair"
	credentialVerificationsEndpoint = "/verifications"
	verifierBasePath                = "/verifier"
	credentialsVerificationEndpoint = verifierBasePath + "/credentials"

	successMsg = "success"
	cslSize    = 50

	// IDMappingStoreName is the name given to the store that contains the VC ID -> EDV document ID mapping.
	IDMappingStoreName = "id-mapping"

	invalidRequestErrMsg = "Invalid request"

	// credential verification checks
	proofCheck = "proof"

	// modes
	issuerMode   = "issuer"
	verifierMode = "verifier"

	// Ed25519VerificationKey supported Verification Key types
	Ed25519VerificationKey = "Ed25519VerificationKey"

	// json keys
	keyID = "kid"
)

var errProfileNotFound = errors.New("specified profile ID does not exist")

// Handler http handler for each controller API endpoint
type Handler interface {
	Path() string
	Method() string
	Handle() http.HandlerFunc
}

type vcStatusManager interface {
	CreateStatusID() (*verifiable.TypedID, error)
	UpdateVCStatus(v *verifiable.Credential, profile *vcprofile.DataProfile, status, statusReason string) error
	GetCSL(id string) (*cslstatus.CSL, error)
}

// EDVClient interface to interact with edv client
type EDVClient interface {
	CreateDataVault(config *operation.DataVaultConfiguration) (string, error)
	CreateDocument(vaultID string, document *operation.EncryptedDocument) (string, error)
	ReadDocument(vaultID, docID string) (*operation.EncryptedDocument, error)
}

type httpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type didBlocClient interface {
	CreateDID(domain string, opts ...didclient.CreateDIDOption) (*did.Doc, error)
}

type kmsProvider struct {
	kms legacykms.KeyManager
}

func (p kmsProvider) LegacyKMS() legacykms.KeyManager {
	return p.kms
}

// New returns CreateCredential instance
func New(config *Config) (*Operation, error) {
	err := config.StoreProvider.CreateStore(credentialStoreName)
	if err != nil {
		if err != storage.ErrDuplicateStore {
			return nil, err
		}
	}

	store, err := config.StoreProvider.OpenStore(credentialStoreName)
	if err != nil {
		return nil, err
	}

	//TODO: Should this be opened in the same store? https://github.com/trustbloc/edge-service/issues/112
	err = config.StoreProvider.CreateStore(IDMappingStoreName)
	if err != nil {
		if err != storage.ErrDuplicateStore {
			return nil, err
		}
	}

	idMappingStore, err := config.StoreProvider.OpenStore(IDMappingStoreName)
	if err != nil {
		return nil, err
	}

	c := crypto.New(config.KMS, verifiable.NewDIDKeyResolver(config.VDRI))

	vcStatusManager, err := cslstatus.New(config.StoreProvider, config.HostURL+vcStatus, cslSize, c)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate new csl status: %w", err)
	}

	kmsProv := kmsProvider{
		kms: config.KMS,
	}

	packer := authcrypt.New(kmsProv)

	_, senderKey, err := config.KMS.CreateKeySet()
	if err != nil {
		return nil, err
	}

	svc := &Operation{
		profileStore:    vcprofile.New(store),
		edvClient:       config.EDVClient,
		kms:             config.KMS,
		vdri:            config.VDRI,
		crypto:          c,
		packer:          packer,
		senderKey:       senderKey,
		vcStatusManager: vcStatusManager,
		didBlocClient:   didclient.New(didclient.WithKMS(config.KMS)),
		domain:          config.Domain,
		idMappingStore:  idMappingStore,
		httpClient:      &http.Client{},
		HostURL:         config.HostURL,
	}

	return svc, nil
}

// Config defines configuration for vcs operations
type Config struct {
	StoreProvider storage.Provider
	EDVClient     EDVClient
	KMS           legacykms.KMS
	VDRI          vdriapi.Registry
	HostURL       string
	Domain        string
	Mode          string
}

// Operation defines handlers for Edge service
type Operation struct {
	profileStore    *vcprofile.Profile
	edvClient       EDVClient
	kms             legacykms.KeyManager
	vdri            vdriapi.Registry
	crypto          *crypto.Crypto
	packer          *authcrypt.Packer
	senderKey       string
	vcStatusManager vcStatusManager
	didBlocClient   didBlocClient
	domain          string
	idMappingStore  storage.Store
	httpClient      httpClient
	HostURL         string
}

// GetRESTHandlers get all controller API handler available for this service
func (o *Operation) GetRESTHandlers(mode string) ([]Handler, error) {
	switch mode {
	case verifierMode:
		return []Handler{
			support.NewHTTPHandler(verifyCredentialEndpoint, http.MethodPost, o.verifyCredentialHandler),
			support.NewHTTPHandler(verifyPresentationEndpoint, http.MethodPost, o.verifyVPHandler),
			// TODO https://github.com/trustbloc/edge-service/issues/153 Remove /verifications API after
			//  transition period
			support.NewHTTPHandler(credentialVerificationsEndpoint, http.MethodPost, o.credentialsVerificationHandler),
			support.NewHTTPHandler(credentialsVerificationEndpoint, http.MethodPost, o.credentialsVerificationHandler),
		}, nil
	case issuerMode:
		return []Handler{
			// profile
			support.NewHTTPHandler(createProfileEndpoint, http.MethodPost, o.createProfileHandler),
			support.NewHTTPHandler(getProfileEndpoint, http.MethodGet, o.getProfileHandler),

			// verifiable credential
			support.NewHTTPHandler(createCredentialEndpoint, http.MethodPost, o.createCredentialHandler),
			support.NewHTTPHandler(storeCredentialEndpoint, http.MethodPost, o.storeVCHandler),
			support.NewHTTPHandler(verifyCredentialEndpoint, http.MethodPost, o.verifyCredentialHandler),
			support.NewHTTPHandler(updateCredentialStatusEndpoint, http.MethodPost, o.updateCredentialStatusHandler),
			support.NewHTTPHandler(retrieveCredentialEndpoint, http.MethodGet, o.retrieveVCHandler),
			support.NewHTTPHandler(vcStatusEndpoint, http.MethodGet, o.vcStatus),

			// issuer apis
			// TODO update trustbloc components to use these APIs instead of above ones
			support.NewHTTPHandler(generateKeypairPath, http.MethodGet, o.generateKeypairHandler),
			support.NewHTTPHandler(issueCredentialPath, http.MethodPost, o.issueCredentialHandler),
			support.NewHTTPHandler(composeAndIssueCredentialPath, http.MethodPost, o.composeAndIssueCredentialHandler),
		}, nil
	default:
		return nil, fmt.Errorf("invalid operation mode: %s", mode)
	}
}

func (o *Operation) vcStatus(rw http.ResponseWriter, req *http.Request) {
	csl, err := o.vcStatusManager.GetCSL(o.HostURL + req.RequestURI)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest,
			fmt.Sprintf("failed to get credential status list: %s", err.Error()))

		return
	}

	rw.WriteHeader(http.StatusOK)
	o.writeResponse(rw, csl)
}

func (o *Operation) createCredentialHandler(rw http.ResponseWriter, req *http.Request) {
	data := CreateCredentialRequest{}

	err := json.NewDecoder(req.Body).Decode(&data)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, fmt.Sprintf(invalidRequestErrMsg+": %s", err.Error()))

		return
	}

	profile, err := o.profileStore.GetProfile(data.Profile)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, fmt.Sprintf("failed to read profile: %s", err.Error()))

		return
	}

	validCredential, err := o.createCredential(profile, &data)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, fmt.Sprintf("failed to create credential: %s", err.Error()))

		return
	}

	signedVC, err := o.crypto.SignCredential(profile, validCredential)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusInternalServerError, fmt.Sprintf("failed to sign credential: %s", err.Error()))

		return
	}

	rw.WriteHeader(http.StatusCreated)
	o.writeResponse(rw, signedVC)
}

func (o *Operation) verifyCredentialHandler(rw http.ResponseWriter, req *http.Request) {
	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest,
			fmt.Sprintf("failed to read request body: %s", err.Error()))

		return
	}

	// verify vc
	vc, err := o.parseAndVerifyVC(body)
	if err != nil {
		response := &VerifyCredentialResponse{
			Verified: false,
			Message:  err.Error()}

		rw.WriteHeader(http.StatusOK)
		o.writeResponse(rw, response)

		return
	}

	// vc is verified
	// now to check vc status
	resp, err := o.checkVCStatus(vc.Status.ID, vc.ID)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusInternalServerError,
			err.Error())

		return
	}

	rw.WriteHeader(http.StatusOK)
	o.writeResponse(rw, resp)
}

func (o *Operation) checkVCStatus(vclID, vcID string) (*VerifyCredentialResponse, error) {
	vcResp := &VerifyCredentialResponse{
		Verified: false}

	req, err := http.NewRequest(http.MethodGet, vclID, nil)
	if err != nil {
		return nil, err
	}

	resp, err := o.sendHTTPRequest(req, http.StatusOK)
	if err != nil {
		return nil, err
	}

	var csl cslstatus.CSL
	if err := json.Unmarshal(resp, &csl); err != nil {
		return nil, fmt.Errorf("failed to unmarshal resp to csl: %w", err)
	}

	for _, vcStatus := range csl.VC {
		if !strings.Contains(vcStatus, vcID) {
			continue
		}

		statusVc, err := o.parseAndVerifyVC([]byte(vcStatus))
		if err != nil {
			return nil, fmt.Errorf("failed to parse and verify status vc: %s", err.Error())
		}

		subjectBytes, err := json.Marshal(statusVc.Subject)
		if err != nil {
			return nil, fmt.Errorf(fmt.Sprintf("failed to marshal status vc subject: %s", err.Error()))
		}

		vcResp.Message = string(subjectBytes)

		return vcResp, nil
	}

	vcResp.Verified = true
	vcResp.Message = successMsg

	return vcResp, nil
}

func (o *Operation) sendHTTPRequest(req *http.Request, status int) ([]byte, error) {
	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer func() {
		err = resp.Body.Close()
		if err != nil {
			log.Warn("failed to close response body")
		}
	}()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Warnf("failed to read response body for status %d: %s", resp.StatusCode, err)
	}

	if resp.StatusCode != status {
		return nil, fmt.Errorf("failed to read response body for status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

func (o *Operation) updateCredentialStatusHandler(rw http.ResponseWriter, req *http.Request) {
	data := UpdateCredentialStatusRequest{}
	err := json.NewDecoder(req.Body).Decode(&data)

	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest,
			fmt.Sprintf("failed to decode request received: %s", err.Error()))
		return
	}

	vc, err := o.parseAndVerifyVC([]byte(data.Credential))
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest,
			fmt.Sprintf("unable to unmarshal the VC: %s", err.Error()))
		return
	}

	// get profile
	profile, err := o.profileStore.GetProfile(vc.Issuer.Name)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest,
			fmt.Sprintf("failed to get profile: %s", err.Error()))
		return
	}

	if err := o.vcStatusManager.UpdateVCStatus(vc, profile, data.Status, data.StatusReason); err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest,
			fmt.Sprintf("failed to update vc status: %s", err.Error()))
		return
	}

	rw.WriteHeader(http.StatusOK)
}

func (o *Operation) createProfileHandler(rw http.ResponseWriter, req *http.Request) {
	data := ProfileRequest{}

	err := json.NewDecoder(req.Body).Decode(&data)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, fmt.Sprintf(invalidRequestErrMsg+": %s", err.Error()))

		return
	}

	profileResponse, err := o.createProfile(&data)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, err.Error())

		return
	}

	rw.WriteHeader(http.StatusCreated)
	o.writeResponse(rw, profileResponse)
}

func (o *Operation) getProfileHandler(rw http.ResponseWriter, req *http.Request) {
	profileID := mux.Vars(req)["id"]

	profileResponseJSON, err := o.profileStore.GetProfile(profileID)
	if err != nil {
		if err == errProfileNotFound {
			o.writeErrorResponse(rw, http.StatusNotFound, "Failed to find the profile")

			return
		}

		o.writeErrorResponse(rw, http.StatusBadRequest, err.Error())

		return
	}

	o.writeResponse(rw, profileResponseJSON)
}

func (o *Operation) storeVCHandler(rw http.ResponseWriter, req *http.Request) {
	data := &StoreVCRequest{}

	err := json.NewDecoder(req.Body).Decode(&data)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, fmt.Sprintf(invalidRequestErrMsg+": %s", err.Error()))

		return
	}

	vc, err := o.parseAndVerifyVC([]byte(data.Credential))
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest,
			fmt.Sprintf("unable to unmarshal the VC: %s", err.Error()))
		return
	}

	if err = validateRequest(data.Profile, vc.ID); err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, err.Error())

		return
	}

	o.storeVC(data, vc, rw)
}

func (o *Operation) storeVC(data *StoreVCRequest, vc *verifiable.Credential, rw http.ResponseWriter) {
	doc, err := o.buildStructuredDoc(data, vc)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, err.Error())

		return
	}

	marshalledStructuredDoc, err := json.Marshal(doc)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, err.Error())

		return
	}

	// We have no recipients, so we pass in the sender key as the recipient key as well
	encryptedStructuredDoc, err := o.packer.Pack(marshalledStructuredDoc,
		base58.Decode(o.senderKey), [][]byte{base58.Decode(o.senderKey)})
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, err.Error())

		return
	}

	encryptedDocument := operation.EncryptedDocument{
		ID:       doc.ID,
		Sequence: 0,
		JWE:      encryptedStructuredDoc,
	}

	_, err = o.edvClient.CreateDocument(data.Profile, &encryptedDocument)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, err.Error())

		return
	}

	err = o.idMappingStore.Put(vc.ID, []byte(doc.ID))
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, err.Error())

		return
	}
}

func (o *Operation) buildStructuredDoc(data *StoreVCRequest,
	vc *verifiable.Credential) (*operation.StructuredDocument, error) {
	var edvDocID string

	idFromMapping, err := o.idMappingStore.Get(vc.ID)
	switch err {
	case storage.ErrValueNotFound:
		edvDocID, err = generateEDVCompatibleID()
		if err != nil {
			return nil, err
		}
	case nil:
		edvDocID = string(idFromMapping)
	default:
		return nil, err
	}

	doc := operation.StructuredDocument{}
	doc.ID = edvDocID
	doc.Content = make(map[string]interface{})

	credentialBytes := []byte(data.Credential)

	var credentialJSONRawMessage json.RawMessage = credentialBytes

	doc.Content["message"] = credentialJSONRawMessage

	return &doc, nil
}

func generateEDVCompatibleID() (string, error) {
	randomBytes := make([]byte, 16)

	_, err := rand.Read(randomBytes)
	if err != nil {
		return "", err
	}

	base58EncodedUUID := base58.Encode(randomBytes)

	return base58EncodedUUID, nil
}

func (o *Operation) retrieveVCHandler(rw http.ResponseWriter, req *http.Request) {
	id := req.URL.Query().Get("id")
	profile := req.URL.Query().Get("profile")

	if err := validateRequest(profile, id); err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, err.Error())

		return
	}

	edvDocID, err := o.idMappingStore.Get(id)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, err.Error())

		return
	}

	document, err := o.edvClient.ReadDocument(profile, string(edvDocID))
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, err.Error())

		return
	}

	decryptedEnvelope, err := o.packer.Unpack(document.JWE)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusInternalServerError,
			fmt.Sprintf("decrypted envelope unpacking failed: %s", err.Error()))

		return
	}

	decryptedDoc := operation.StructuredDocument{}

	err = json.Unmarshal(decryptedEnvelope.Message, &decryptedDoc)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusInternalServerError,
			fmt.Sprintf("decrypted structured document unmarshalling failed: %s", err.Error()))

		return
	}

	responseMsg, err := json.Marshal(decryptedDoc.Content["message"])
	if err != nil {
		o.writeErrorResponse(rw, http.StatusInternalServerError,
			fmt.Sprintf("structured document content marshalling failed: %s", err.Error()))

		return
	}

	_, err = rw.Write(responseMsg)
	if err != nil {
		log.Errorf("Failed to write response for document retrieval success: %s",
			err.Error())

		return
	}
}

func (o *Operation) verifyVPHandler(rw http.ResponseWriter, req *http.Request) {
	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest,
			fmt.Sprintf("failed to read request body: %s", err.Error()))

		return
	}
	// verify vp
	_, err = o.parseAndVerifyVP(body)
	if err != nil {
		response := &VerifyCredentialResponse{
			Verified: false,
			Message:  err.Error()}

		rw.WriteHeader(http.StatusOK)
		o.writeResponse(rw, response)

		return
	}

	resp := &VerifyCredentialResponse{
		Verified: true,
		Message:  successMsg,
	}

	rw.WriteHeader(http.StatusOK)
	o.writeResponse(rw, resp)
}

func (o *Operation) createCredential(profile *vcprofile.DataProfile,
	data *CreateCredentialRequest) (*verifiable.Credential, error) {
	credential := &verifiable.Credential{}

	issueDate := time.Now().UTC()

	credential.Context = data.Context
	credential.Subject = data.Subject
	credential.Types = data.Type
	credential.Issuer = verifiable.Issuer{
		ID:   profile.DID,
		Name: profile.Name,
	}
	credential.Issued = &issueDate
	credential.ID = profile.URI + "/" + uuid.New().String()

	var err error

	credential.Status, err = o.vcStatusManager.CreateStatusID()
	if err != nil {
		return nil, fmt.Errorf("failed to create status id for vc: %w", err)
	}

	cred, err := json.Marshal(credential)
	if err != nil {
		return nil, fmt.Errorf("create credential marshalling failed: %s", err.Error())
	}

	validatedCred, _, err := verifiable.NewCredential(cred)
	if err != nil {
		return nil, fmt.Errorf("failed to create new credential: %s", err.Error())
	}

	return validatedCred, nil
}

func (o *Operation) createProfile(pr *ProfileRequest) (*vcprofile.DataProfile, error) {
	if err := validateProfileRequest(pr); err != nil {
		return nil, err
	}

	var didDoc *did.Doc

	var err error

	if pr.DID == "" {
		didDoc, err = o.didBlocClient.CreateDID(o.domain)
		if err != nil {
			return nil, fmt.Errorf("failed to create did doc: %v", err)
		}
	} else {
		didDoc, err = o.vdri.Resolve(pr.DID)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve did: %v", err)
		}
	}

	publicKeyID, err := getPublicKeyID(didDoc)
	if err != nil {
		return nil, err
	}

	created := time.Now().UTC()
	profileResponse := &vcprofile.DataProfile{
		Name:                    pr.Name,
		URI:                     pr.URI,
		Created:                 &created,
		DID:                     didDoc.ID,
		SignatureType:           pr.SignatureType,
		SignatureRepresentation: pr.SignatureRepresentation,
		Creator:                 publicKeyID,
		DIDPrivateKey:           pr.DIDPrivateKey,
	}

	err = o.profileStore.SaveProfile(profileResponse)
	if err != nil {
		return nil, err
	}

	// create the vault associated with the profile
	_, err = o.edvClient.CreateDataVault(&operation.DataVaultConfiguration{ReferenceID: pr.Name})
	if err != nil {
		return nil, err
	}

	return profileResponse, nil
}

func validateProfileRequest(pr *ProfileRequest) error {
	if pr.Name == "" {
		return fmt.Errorf("missing profile name")
	}

	if pr.URI == "" {
		return fmt.Errorf("missing URI information")
	}

	if pr.SignatureType == "" {
		return fmt.Errorf("missing signature type")
	}

	_, err := url.Parse(pr.URI)
	if err != nil {
		return fmt.Errorf("invalid uri: %s", err.Error())
	}

	return nil
}

func validateRequest(profileName, vcID string) error {
	if profileName == "" {
		return fmt.Errorf("missing profile name")
	}

	if vcID == "" {
		return fmt.Errorf("missing verifiable credential ID")
	}

	return nil
}

// writeResponse writes interface value to response
func (o *Operation) writeResponse(rw io.Writer, v interface{}) {
	err := json.NewEncoder(rw).Encode(v)
	if err != nil {
		log.Errorf("Unable to send error response, %s", err)
	}
}

func (o *Operation) writeErrorResponse(rw http.ResponseWriter, status int, msg string) {
	rw.WriteHeader(status)

	if _, err := rw.Write([]byte(msg)); err != nil {
		log.Errorf("Unable to send error message, %s", err)
	}
}

func (o *Operation) issueCredentialHandler(rw http.ResponseWriter, req *http.Request) {
	// get the request
	cred := IssueCredentialRequest{}

	err := json.NewDecoder(req.Body).Decode(&cred)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, fmt.Sprintf(invalidRequestErrMsg+": %s", err.Error()))

		return
	}

	// validate the VC
	validatedCred, _, err := verifiable.NewCredential(cred.Credential)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, fmt.Sprintf("failed to validate credential: %s", err.Error()))

		return
	}

	// sign the credential
	signedVC, err := o.signCredential(validatedCred, cred.Opts.AssertionMethod, verifiable.SignatureJWS)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusInternalServerError, fmt.Sprintf("failed to sign credential:"+
			" %s", err.Error()))

		return
	}

	rw.WriteHeader(http.StatusOK)
	o.writeResponse(rw, signedVC)
}

func (o *Operation) composeAndIssueCredentialHandler(rw http.ResponseWriter, req *http.Request) {
	// get the request
	composeCredReq := ComposeCredentialRequest{}

	err := json.NewDecoder(req.Body).Decode(&composeCredReq)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, fmt.Sprintf(invalidRequestErrMsg+": %s", err.Error()))

		return
	}

	// create the verifiable credential
	credential, err := buildCredential(&composeCredReq)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, fmt.Sprintf("failed to build credential:"+
			" %s", err.Error()))

		return
	}

	signatureRepresentation := verifiable.SignatureJWS

	if composeCredReq.ProofFormat != "" {
		switch composeCredReq.ProofFormat {
		case "jws":
			signatureRepresentation = verifiable.SignatureJWS
		case "proofValue":
			signatureRepresentation = verifiable.SignatureProofValue
		default:
			o.writeErrorResponse(rw, http.StatusBadRequest, fmt.Sprintf("invalid proof format : %s",
				composeCredReq.ProofFormat))

			return
		}
	}

	signerDID, err := getSignerDID(&composeCredReq)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, fmt.Sprintf("failed to get DID for signing:"+
			" %s", err.Error()))

		return
	}

	// sign the credential
	signedVC, err := o.signCredential(credential, signerDID, signatureRepresentation)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusInternalServerError, fmt.Sprintf("failed to sign credential:"+
			" %s", err.Error()))

		return
	}

	// response
	rw.WriteHeader(http.StatusOK)
	o.writeResponse(rw, signedVC)
}

func (o *Operation) signCredential(credential *verifiable.Credential, didID string,
	signRepresentation verifiable.SignatureRepresentation) (*verifiable.Credential, error) {
	// Resolve DID and get the public keyID
	didDoc, err := o.vdri.Resolve(didID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve DID : %s", err.Error())
	}

	publicKeyID, err := getPublicKeyID(didDoc)
	if err != nil {
		return nil, err
	}

	// sign the credential
	signedVC, err := o.crypto.SignCredential(
		&vcprofile.DataProfile{
			Creator: publicKeyID,
			// TODO https://github.com/trustbloc/edge-service/issues/125 passed as options to request or
			//  set by environment variables ?
			SignatureType:           "Ed25519Signature2018",
			SignatureRepresentation: signRepresentation,
		},
		credential,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to sign credential: %s", err.Error())
	}

	return signedVC, nil
}

func buildCredential(composeCredReq *ComposeCredentialRequest) (*verifiable.Credential, error) {
	// create the verifiable credential
	credential := &verifiable.Credential{}

	// set credential data
	credential.Context = []string{"https://www.w3.org/2018/credentials/v1"}
	credential.Issued = composeCredReq.IssuanceDate
	credential.Expired = composeCredReq.ExpirationDate

	// set default type, if request doesn't contain the type
	credential.Types = []string{"VerifiableCredential"}
	if len(composeCredReq.Types) != 0 {
		credential.Types = composeCredReq.Types
	}

	// set subject
	credentialSubject := make(map[string]interface{})

	if composeCredReq.Claims != nil {
		err := json.Unmarshal(composeCredReq.Claims, &credentialSubject)
		if err != nil {
			return nil, err
		}
	}

	credentialSubject["id"] = composeCredReq.Subject
	credential.Subject = credentialSubject

	// set issuer
	credential.Issuer = verifiable.Issuer{
		ID: composeCredReq.Issuer,
	}

	// set terms of use
	termsOfUse, err := decodeTypedID(composeCredReq.TermsOfUse)
	if err != nil {
		return nil, err
	}

	credential.TermsOfUse = termsOfUse

	// set evidence
	if composeCredReq.Evidence != nil {
		evidence := make(map[string]interface{})

		err := json.Unmarshal(composeCredReq.Evidence, &evidence)
		if err != nil {
			return nil, err
		}

		credential.Evidence = evidence
	}

	return credential, nil
}

func decodeTypedID(bytes json.RawMessage) ([]verifiable.TypedID, error) {
	if len(bytes) == 0 {
		return nil, nil
	}

	var singleTypedID verifiable.TypedID

	err := json.Unmarshal(bytes, &singleTypedID)
	if err == nil {
		return []verifiable.TypedID{singleTypedID}, nil
	}

	var composedTypedID []verifiable.TypedID

	err = json.Unmarshal(bytes, &composedTypedID)
	if err == nil {
		return composedTypedID, nil
	}

	return nil, err
}

func getSignerDID(composeCredReq *ComposeCredentialRequest) (string, error) {
	signerDID := composeCredReq.Issuer

	if composeCredReq.ProofFormatOptions != nil {
		proofFormatOptions := make(map[string]interface{})

		err := json.Unmarshal(composeCredReq.ProofFormatOptions, &proofFormatOptions)
		if err != nil {
			return "", err
		}

		if proofFormatOptions[keyID] != "" {
			kid, ok := proofFormatOptions[keyID].(string)
			if !ok {
				return "", errors.New("invalid kid type")
			}

			signerDID = kid
		}
	}

	return signerDID, nil
}

func (o *Operation) generateKeypairHandler(rw http.ResponseWriter, req *http.Request) {
	_, signKey, err := o.kms.CreateKeySet()
	if err != nil {
		o.writeErrorResponse(rw, http.StatusInternalServerError,
			fmt.Sprintf("failed to create key pair: %s", err.Error()))

		return
	}

	rw.WriteHeader(http.StatusOK)
	o.writeResponse(rw, &GenerateKeyPairResponse{
		PublicKey: signKey,
	})
}

func (o *Operation) credentialsVerificationHandler(rw http.ResponseWriter, req *http.Request) {
	// get the request
	verificationReq := CredentialsVerificationRequest{}

	err := json.NewDecoder(req.Body).Decode(&verificationReq)
	if err != nil {
		o.writeErrorResponse(rw, http.StatusBadRequest, fmt.Sprintf(invalidRequestErrMsg+": %s", err.Error()))

		return
	}

	checks := []string{proofCheck}

	// if req contains checks, then override the default checks
	if verificationReq.Opts != nil && len(verificationReq.Opts.Checks) != 0 {
		checks = verificationReq.Opts.Checks
	}

	var result []CredentialsVerificationCheckResult

	for _, val := range checks {
		switch val {
		case proofCheck:
			err := o.checkProof(verificationReq.Credential)
			if err != nil {
				result = append(result, CredentialsVerificationCheckResult{
					Check: val,
					Error: err.Error(),
				})
			}
		default:
			result = append(result, CredentialsVerificationCheckResult{
				Check: val,
				Error: "check not supported",
			})
		}
	}

	if len(result) == 0 {
		rw.WriteHeader(http.StatusOK)
		o.writeResponse(rw, &CredentialsVerificationSuccessResponse{
			Checks: checks,
		})
	} else {
		rw.WriteHeader(http.StatusBadRequest)
		o.writeResponse(rw, &CredentialsVerificationFailResponse{
			Checks: result,
		})
	}
}

func (o *Operation) checkProof(vcByte []byte) error {
	suite := ed25519signature2018.New(ed25519signature2018.WithVerifier(&ed25519signature2018.PublicKeyVerifier{}))
	vc, _, err := verifiable.NewCredential(
		vcByte,
		verifiable.WithEmbeddedSignatureSuites(suite),
		verifiable.WithPublicKeyFetcher(
			verifiable.NewDIDKeyResolver(o.vdri).PublicKeyFetcher(),
		),
	)

	if err != nil {
		return fmt.Errorf("proof validation error : %w", err)
	}

	if len(vc.Proofs) == 0 {
		return errors.New("verifiable credential doesn't contains proof")
	}

	return nil
}

func (o *Operation) parseAndVerifyVC(vcBytes []byte) (*verifiable.Credential, error) {
	suite := ed25519signature2018.New(ed25519signature2018.WithVerifier(&ed25519signature2018.PublicKeyVerifier{}))
	vc, _, err := verifiable.NewCredential(
		vcBytes,
		verifiable.WithEmbeddedSignatureSuites(suite),
		verifiable.WithPublicKeyFetcher(
			verifiable.NewDIDKeyResolver(o.vdri).PublicKeyFetcher(),
		),
	)

	if err != nil {
		return nil, err
	}

	return vc, nil
}

func (o *Operation) parseAndVerifyVP(vpBytes []byte) (*verifiable.Presentation, error) {
	suite := ed25519signature2018.New(ed25519signature2018.WithVerifier(&ed25519signature2018.PublicKeyVerifier{}))
	vp, err := verifiable.NewPresentation(
		vpBytes,
		verifiable.WithPresEmbeddedSignatureSuites(suite),
		verifiable.WithPresPublicKeyFetcher(
			verifiable.NewDIDKeyResolver(o.vdri).PublicKeyFetcher(),
		),
	)

	if err != nil {
		return nil, err
	}
	// vp is verified

	// verify if the credentials in vp are valid
	for _, cred := range vp.Credentials() {
		vcBytes, err := json.Marshal(cred)
		if err != nil {
			return nil, err
		}
		// verify if the credential in vp is valid
		_, err = o.parseAndVerifyVC(vcBytes)
		if err != nil {
			return nil, err
		}
	}

	return vp, nil
}

func getPublicKeyID(didDoc *did.Doc) (string, error) {
	switch {
	case len(didDoc.PublicKey) > 0:
		var publicKeyID string

		for _, k := range didDoc.PublicKey {
			if strings.HasPrefix(k.Type, Ed25519VerificationKey) {
				publicKeyID = k.ID
				break
			}
		}

		// TODO this is temporary check to support public key ID's which aren't in DID format
		// Will be removed [Issue#140]
		if !isDID(publicKeyID) {
			return didDoc.ID + publicKeyID, nil
		}

		return publicKeyID, nil
	case len(didDoc.Authentication) > 0:
		return didDoc.Authentication[0].PublicKey.ID, nil
	default:
		return "", errors.New("public key not found in DID Document")
	}
}

func isDID(str string) bool {
	return strings.HasPrefix(str, "did:")
}
