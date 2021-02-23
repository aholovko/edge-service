// Code generated by go-swagger; DO NOT EDIT.

// /*
// Copyright SecureKey Technologies Inc. All Rights Reserved.
//
// SPDX-License-Identifier: Apache-2.0
// */
//

package operations

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"context"
	"net/http"
	"time"

	"github.com/go-openapi/errors"
	"github.com/go-openapi/runtime"
	cr "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"

	"github.com/trustbloc/edge-service/pkg/client/csh/models"
)

// NewPostHubstoreProfilesParams creates a new PostHubstoreProfilesParams object,
// with the default timeout for this client.
//
// Default values are not hydrated, since defaults are normally applied by the API server side.
//
// To enforce default values in parameter, use SetDefaults or WithDefaults.
func NewPostHubstoreProfilesParams() *PostHubstoreProfilesParams {
	return &PostHubstoreProfilesParams{
		timeout: cr.DefaultTimeout,
	}
}

// NewPostHubstoreProfilesParamsWithTimeout creates a new PostHubstoreProfilesParams object
// with the ability to set a timeout on a request.
func NewPostHubstoreProfilesParamsWithTimeout(timeout time.Duration) *PostHubstoreProfilesParams {
	return &PostHubstoreProfilesParams{
		timeout: timeout,
	}
}

// NewPostHubstoreProfilesParamsWithContext creates a new PostHubstoreProfilesParams object
// with the ability to set a context for a request.
func NewPostHubstoreProfilesParamsWithContext(ctx context.Context) *PostHubstoreProfilesParams {
	return &PostHubstoreProfilesParams{
		Context: ctx,
	}
}

// NewPostHubstoreProfilesParamsWithHTTPClient creates a new PostHubstoreProfilesParams object
// with the ability to set a custom HTTPClient for a request.
func NewPostHubstoreProfilesParamsWithHTTPClient(client *http.Client) *PostHubstoreProfilesParams {
	return &PostHubstoreProfilesParams{
		HTTPClient: client,
	}
}

/* PostHubstoreProfilesParams contains all the parameters to send to the API endpoint
   for the post hubstore profiles operation.

   Typically these are written to a http.Request.
*/
type PostHubstoreProfilesParams struct {

	// Request.
	Request *models.Profile

	timeout    time.Duration
	Context    context.Context
	HTTPClient *http.Client
}

// WithDefaults hydrates default values in the post hubstore profiles params (not the query body).
//
// All values with no default are reset to their zero value.
func (o *PostHubstoreProfilesParams) WithDefaults() *PostHubstoreProfilesParams {
	o.SetDefaults()
	return o
}

// SetDefaults hydrates default values in the post hubstore profiles params (not the query body).
//
// All values with no default are reset to their zero value.
func (o *PostHubstoreProfilesParams) SetDefaults() {
	// no default values defined for this parameter
}

// WithTimeout adds the timeout to the post hubstore profiles params
func (o *PostHubstoreProfilesParams) WithTimeout(timeout time.Duration) *PostHubstoreProfilesParams {
	o.SetTimeout(timeout)
	return o
}

// SetTimeout adds the timeout to the post hubstore profiles params
func (o *PostHubstoreProfilesParams) SetTimeout(timeout time.Duration) {
	o.timeout = timeout
}

// WithContext adds the context to the post hubstore profiles params
func (o *PostHubstoreProfilesParams) WithContext(ctx context.Context) *PostHubstoreProfilesParams {
	o.SetContext(ctx)
	return o
}

// SetContext adds the context to the post hubstore profiles params
func (o *PostHubstoreProfilesParams) SetContext(ctx context.Context) {
	o.Context = ctx
}

// WithHTTPClient adds the HTTPClient to the post hubstore profiles params
func (o *PostHubstoreProfilesParams) WithHTTPClient(client *http.Client) *PostHubstoreProfilesParams {
	o.SetHTTPClient(client)
	return o
}

// SetHTTPClient adds the HTTPClient to the post hubstore profiles params
func (o *PostHubstoreProfilesParams) SetHTTPClient(client *http.Client) {
	o.HTTPClient = client
}

// WithRequest adds the request to the post hubstore profiles params
func (o *PostHubstoreProfilesParams) WithRequest(request *models.Profile) *PostHubstoreProfilesParams {
	o.SetRequest(request)
	return o
}

// SetRequest adds the request to the post hubstore profiles params
func (o *PostHubstoreProfilesParams) SetRequest(request *models.Profile) {
	o.Request = request
}

// WriteToRequest writes these params to a swagger request
func (o *PostHubstoreProfilesParams) WriteToRequest(r runtime.ClientRequest, reg strfmt.Registry) error {

	if err := r.SetTimeout(o.timeout); err != nil {
		return err
	}
	var res []error
	if o.Request != nil {
		if err := r.SetBodyParam(o.Request); err != nil {
			return err
		}
	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}