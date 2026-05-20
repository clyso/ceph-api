package auth

import (
	"fmt"
	"net/http"

	"github.com/ory/fosite"
	"github.com/ory/fosite/handler/oauth2"
	"github.com/rs/zerolog"
)

func (s *Server) TokenEndpoint(rw http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	logger := zerolog.Ctx(ctx)

	// Create an empty session object that will be passed to storage implementation to populate (unmarshal) the session into.
	// By passing an empty session object as a "prototype" to the store, the store can use the underlying type to unmarshal the value into it.
	// For an example of storage implementation that takes advantage of that, see SQL Store (fosite_store_sql.go) from ory/Hydra project.
	sessionData := s.newSession("", nil)

	// Next we create a response for the access request. Again, we iterate through the TokenEndpointHandlers
	// and aggregate the result in response.
	accessRequest, err := s.provider.NewAccessRequest(ctx, req, sessionData)
	// Catch any errors, e.g.:
	// * unknown client
	// * invalid redirect
	// * ...
	if err != nil {
		fositeError := fosite.ErrorToRFC6749Error(err)
		if !fositeError.Is(fosite.ErrInvalidGrant) {
			logger.Debug().Str("error_description", fositeError.GetDescription()).Err(err).Msg("error occurred in NewAccessRequest")
		}
		s.provider.WriteAccessError(ctx, rw, accessRequest, err)
		return
	}

	username := sessionData.GetUsername()
	if username == "" {
		username = accessRequest.GetRequestForm().Get("username")
	}

	usr, err := s.userSvc.GetUser(ctx, username)
	if err != nil {
		fositeError := fosite.ErrorToRFC6749Error(err)
		logger.Error().Str("error_description", fositeError.GetDescription()).Err(err).Msg("can't find account")
		s.provider.WriteAccessError(ctx, rw, accessRequest, err)
		return
	}
	if !usr.Enabled {
		err = fmt.Errorf("inactive account")
		fositeError := fosite.ErrorToRFC6749Error(err)
		logger.Error().Str("error_description", fositeError.GetDescription()).Err(err).Msg("account is inactive")
		s.provider.WriteAccessError(ctx, rw, accessRequest, err)
		return
	}
	session := accessRequest.GetSession().(*oauth2.JWTSession)
	// fosite sets session.Subject via SetSubject after Authenticate (password grant);
	// the JWT "sub" claim is built from JWTClaims.Subject, which we must populate here.
	session.JWTClaims.Subject = usr.Username

	// Next we create a response for the access request. Again, we iterate through the TokenEndpointHandlers
	// and aggregate the result in response.
	response, err := s.provider.NewAccessResponse(ctx, accessRequest)
	if err != nil {
		fositeError := fosite.ErrorToRFC6749Error(err)
		logger.Error().Str("error_description", fositeError.GetDescription()).Err(err).Msg("can't create new access response")
		s.provider.WriteAccessError(ctx, rw, accessRequest, err)
		return
	}

	// Set refresh token lifespan in token response
	response.SetExtra("refresh_expires_in", s.refreshTokenLifespan.Seconds())

	// All done, send the response.
	s.provider.WriteAccessResponse(ctx, rw, accessRequest, response)
}
