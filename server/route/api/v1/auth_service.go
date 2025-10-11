package v1

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/warthurton/slash/internal/util"
	"github.com/warthurton/slash/plugin/idp"
	"github.com/warthurton/slash/plugin/idp/oauth2"
	v1pb "github.com/warthurton/slash/proto/gen/api/v1"
	storepb "github.com/warthurton/slash/proto/gen/store"
	"github.com/warthurton/slash/server/service/license"
	"github.com/warthurton/slash/store"
)

const (
	unmatchedEmailAndPasswordError = "unmatched email and password"
)

func (s *APIV1Service) GetAuthStatus(ctx context.Context, _ *v1pb.GetAuthStatusRequest) (*v1pb.User, error) {
	user, err := getCurrentUser(ctx, s.Store)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get current user: %v", err)
	}
	if user == nil {
		return nil, status.Errorf(codes.Unauthenticated, "user not found")
	}
	return convertUserFromStore(user), nil
}

func (s *APIV1Service) SignIn(ctx context.Context, request *v1pb.SignInRequest) (*v1pb.User, error) {
	user, err := s.Store.GetUser(ctx, &store.FindUser{
		Email: &request.Email,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get user: %v", err)
	}
	if user == nil {
		return nil, status.Errorf(codes.InvalidArgument, unmatchedEmailAndPasswordError)
	}
	// Compare the stored hashed password, with the hashed version of the password that was received.
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(request.Password)); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, unmatchedEmailAndPasswordError)
	}

	workspaceSecuritySetting, err := s.Store.GetWorkspaceSecuritySetting(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get workspace security setting: %v", err)
	}
	if workspaceSecuritySetting.DisallowPasswordAuth && user.Role == store.RoleUser {
		return nil, status.Errorf(codes.PermissionDenied, "password authentication is not allowed")
	}
	if user.RowStatus == storepb.RowStatus_ARCHIVED {
		return nil, status.Errorf(codes.PermissionDenied, "user has been archived")
	}

	if err := s.doSignIn(ctx, user, time.Now().Add(AccessTokenDuration)); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to sign in: %v", err)
	}
	return convertUserFromStore(user), nil
}

func (s *APIV1Service) SignInWithSSO(ctx context.Context, request *v1pb.SignInWithSSORequest) (*v1pb.User, error) {
	if !s.LicenseService.IsFeatureEnabled(license.FeatureTypeSSO) {
		return nil, status.Errorf(codes.PermissionDenied, "SSO is not available in the current plan")
	}

	identityProviderSetting, err := s.Store.GetWorkspaceSetting(ctx, &store.FindWorkspaceSetting{
		Key: storepb.WorkspaceSettingKey_WORKSPACE_SETTING_IDENTITY_PROVIDER,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get workspace setting, err: %s", err)
	}
	if identityProviderSetting == nil || identityProviderSetting.GetIdentityProvider() == nil {
		return nil, status.Errorf(codes.InvalidArgument, "identity provider not found")
	}
	var identityProvider *storepb.IdentityProvider
	for _, idp := range identityProviderSetting.GetIdentityProvider().IdentityProviders {
		if idp.Id == request.IdpId {
			identityProvider = idp
			break
		}
	}
	if identityProvider == nil {
		return nil, status.Errorf(codes.InvalidArgument, "identity provider not found")
	}

	var userInfo *idp.IdentityProviderUserInfo
	if identityProvider.Type == storepb.IdentityProvider_OAUTH2 {
		oauth2IdentityProvider, err := oauth2.NewIdentityProvider(identityProvider.Config.GetOauth2())
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to create oauth2 identity provider, err: %s", err)
		}
		token, err := oauth2IdentityProvider.ExchangeToken(ctx, request.RedirectUri, request.Code)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to exchange token, err: %s", err)
		}
		userInfo, err = oauth2IdentityProvider.UserInfo(token)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to get user info, err: %s", err)
		}
	}

	email := userInfo.Identifier
	if !util.ValidateEmail(email) {
		return nil, status.Errorf(codes.InvalidArgument, "invalid email address")
	}
	user, err := s.Store.GetUser(ctx, &store.FindUser{
		Email: &email,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get user, err: %s", err)
	}
	if user == nil {
		if err := s.checkSeatAvailability(ctx); err != nil {
			return nil, err
		}
		userCreate := &store.User{
			Email:    email,
			Nickname: userInfo.DisplayName,
			// The new signup user should be normal user by default.
			Role: store.RoleUser,
		}
		password, err := util.RandomString(20)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to generate random password, err: %s", err)
		}
		passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to generate password hash, err: %s", err)
		}
		userCreate.PasswordHash = string(passwordHash)
		user, err = s.Store.CreateUser(ctx, userCreate)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to create user, err: %s", err)
		}
	}
	if user.RowStatus == storepb.RowStatus_ARCHIVED {
		return nil, status.Errorf(codes.PermissionDenied, "user has been archived")
	}

	if err := s.doSignIn(ctx, user, time.Now().Add(AccessTokenDuration)); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to sign in, err: %s", err)
	}
	return convertUserFromStore(user), nil
}

func (s *APIV1Service) SignUp(ctx context.Context, request *v1pb.SignUpRequest) (*v1pb.User, error) {
	workspaceSecuritySetting, err := s.Store.GetWorkspaceSecuritySetting(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get workspace security setting: %v", err)
	}
	if workspaceSecuritySetting.DisallowUserRegistration {
		return nil, status.Errorf(codes.PermissionDenied, "sign up is not allowed")
	}

	// Check if the number of users has reached the maximum.
	if err := s.checkSeatAvailability(ctx); err != nil {
		return nil, err
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to generate password hash: %v", err)
	}

	create := &store.User{
		Email:        request.Email,
		Nickname:     request.Nickname,
		PasswordHash: string(passwordHash),
	}
	existingUsers, err := s.Store.ListUsers(ctx, &store.FindUser{})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list users: %v", err)
	}
	// The first user to sign up is an admin by default.
	if len(existingUsers) == 0 {
		create.Role = store.RoleAdmin
	} else {
		create.Role = store.RoleUser
	}

	user, err := s.Store.CreateUser(ctx, create)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create user: %v", err)
	}
	if err := s.doSignIn(ctx, user, time.Now().Add(AccessTokenDuration)); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to sign in: %v", err)
	}
	return convertUserFromStore(user), nil
}

func (s *APIV1Service) doSignIn(ctx context.Context, user *store.User, expireTime time.Time) error {
	accessToken, err := GenerateAccessToken(user.Email, user.ID, expireTime, []byte(s.Secret))
	if err != nil {
		return status.Errorf(codes.Internal, "failed to generate access token: %v", err)
	}
	if err := s.UpsertAccessTokenToStore(ctx, user, accessToken, "user login"); err != nil {
		return status.Errorf(codes.Internal, "failed to upsert access token to store: %v", err)
	}

	cookie := fmt.Sprintf("%s=%s; Path=/; Expires=%s; HttpOnly; SameSite=Strict", AccessTokenCookieName, accessToken, time.Now().Add(AccessTokenDuration).Format(time.RFC1123))
	if err := grpc.SetHeader(ctx, metadata.New(map[string]string{
		"Set-Cookie": cookie,
	})); err != nil {
		return status.Errorf(codes.Internal, "failed to set grpc header, error: %v", err)
	}

	return nil
}

func (*APIV1Service) SignOut(ctx context.Context, _ *v1pb.SignOutRequest) (*emptypb.Empty, error) {
	// Set the cookie header to expire access token.
	if err := grpc.SetHeader(ctx, metadata.New(map[string]string{
		"Set-Cookie": fmt.Sprintf("%s=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT; HttpOnly; SameSite=Strict", AccessTokenCookieName),
	})); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to set grpc header, error: %v", err)
	}
	return &emptypb.Empty{}, nil
}

func (s *APIV1Service) checkSeatAvailability(ctx context.Context) error {
	if !s.LicenseService.IsFeatureEnabled(license.FeatureTypeUnlimitedAccounts) {
		userList, err := s.Store.ListUsers(ctx, &store.FindUser{})
		if err != nil {
			return status.Errorf(codes.Internal, "failed to list users: %v", err)
		}
		seats := s.LicenseService.GetSubscription().Seats
		if len(userList) >= int(seats) {
			return status.Errorf(codes.FailedPrecondition, "maximum number of users %d reached", seats)
		}
	}
	return nil
}
