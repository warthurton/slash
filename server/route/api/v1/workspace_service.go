package v1

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1pb "github.com/warthurton/slash/proto/gen/api/v1"
	storepb "github.com/warthurton/slash/proto/gen/store"
	"github.com/warthurton/slash/store"
)

func (s *APIV1Service) GetWorkspaceProfile(ctx context.Context, _ *v1pb.GetWorkspaceProfileRequest) (*v1pb.WorkspaceProfile, error) {
	workspaceProfile := &v1pb.WorkspaceProfile{
		Mode:    s.Profile.Mode,
		Version: s.Profile.Version,
	}

	// Load subscription plan from license service.
	subscription := s.LicenseService.GetSubscription()
	workspaceProfile.Subscription = subscription

	owner, err := s.GetInstanceOwner(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get instance owner: %v", err)
	}
	if owner != nil {
		workspaceProfile.Owner = fmt.Sprintf("%s%d", UserNamePrefix, owner.Id)
	}

	workspaceGeneralSetting, err := s.Store.GetWorkspaceGeneralSetting(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get workspace general setting")
	}
	workspaceProfile.Branding = workspaceGeneralSetting.GetBranding()

	return workspaceProfile, nil
}

func (s *APIV1Service) GetWorkspaceSetting(ctx context.Context, _ *v1pb.GetWorkspaceSettingRequest) (*v1pb.WorkspaceSetting, error) {
	currentUser, err := getCurrentUser(ctx, s.Store)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get current user: %v", err)
	}
	workspaceSettings, err := s.Store.ListWorkspaceSettings(ctx, &store.FindWorkspaceSetting{})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list workspace settings: %v", err)
	}
	workspaceSetting := &v1pb.WorkspaceSetting{}
	for _, v := range workspaceSettings {
		if v.Key == storepb.WorkspaceSettingKey_WORKSPACE_SETTING_GENERAL {
			generalSetting := v.GetGeneral()
			workspaceSetting.Branding = generalSetting.GetBranding()
			workspaceSetting.CustomStyle = generalSetting.GetCustomStyle()
		} else if v.Key == storepb.WorkspaceSettingKey_WORKSPACE_SETTING_SECURITY {
			securitySetting := v.GetSecurity()
			workspaceSetting.DisallowUserRegistration = securitySetting.GetDisallowUserRegistration()
			workspaceSetting.DisallowPasswordAuth = securitySetting.GetDisallowPasswordAuth()
		} else if v.Key == storepb.WorkspaceSettingKey_WORKSPACE_SETTING_SHORTCUT_RELATED {
			shortcutRelatedSetting := v.GetShortcutRelated()
			workspaceSetting.DefaultVisibility = convertVisibilityFromStorepb(shortcutRelatedSetting.GetDefaultVisibility())
		} else if v.Key == storepb.WorkspaceSettingKey_WORKSPACE_SETTING_IDENTITY_PROVIDER {
			identityProviderSetting := v.GetIdentityProvider()
			workspaceSetting.IdentityProviders = []*v1pb.IdentityProvider{}
			for _, identityProvider := range identityProviderSetting.GetIdentityProviders() {
				identityProviderV1pb := convertIdentityProviderFromStore(identityProvider)
				if currentUser == nil || currentUser.Role != store.RoleAdmin {
					oauth2Config := identityProviderV1pb.Config.GetOauth2()
					if oauth2Config != nil {
						oauth2Config.ClientSecret = ""
					}
				}
				workspaceSetting.IdentityProviders = append(workspaceSetting.IdentityProviders, identityProviderV1pb)
			}
		}
	}
	return workspaceSetting, nil
}

func (s *APIV1Service) UpdateWorkspaceSetting(ctx context.Context, request *v1pb.UpdateWorkspaceSettingRequest) (*v1pb.WorkspaceSetting, error) {
	if request.UpdateMask == nil || len(request.UpdateMask.Paths) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "update mask is empty")
	}

	for _, path := range request.UpdateMask.Paths {
		if path == "branding" {
			generalSetting, err := s.Store.GetWorkspaceGeneralSetting(ctx)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "failed to get workspace setting: %v", err)
			}
			generalSetting.Branding = request.Setting.Branding
			if _, err := s.Store.UpsertWorkspaceSetting(ctx, &storepb.WorkspaceSetting{
				Key: storepb.WorkspaceSettingKey_WORKSPACE_SETTING_GENERAL,
				Value: &storepb.WorkspaceSetting_General{
					General: generalSetting,
				},
			}); err != nil {
				return nil, status.Errorf(codes.Internal, "failed to update workspace setting: %v", err)
			}
		} else if path == "custom_style" {
			generalSetting, err := s.Store.GetWorkspaceGeneralSetting(ctx)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "failed to get workspace setting: %v", err)
			}
			generalSetting.CustomStyle = request.Setting.CustomStyle
			if _, err := s.Store.UpsertWorkspaceSetting(ctx, &storepb.WorkspaceSetting{
				Key: storepb.WorkspaceSettingKey_WORKSPACE_SETTING_GENERAL,
				Value: &storepb.WorkspaceSetting_General{
					General: generalSetting,
				},
			}); err != nil {
				return nil, status.Errorf(codes.Internal, "failed to update workspace setting: %v", err)
			}
		} else if path == "default_visibility" {
			shortcutRelatedSetting, err := s.Store.GetWorkspaceSetting(ctx, &store.FindWorkspaceSetting{
				Key: storepb.WorkspaceSettingKey_WORKSPACE_SETTING_SHORTCUT_RELATED,
			})
			if err != nil {
				return nil, status.Errorf(codes.Internal, "failed to get workspace setting: %v", err)
			}
			if shortcutRelatedSetting == nil {
				shortcutRelatedSetting = &storepb.WorkspaceSetting{
					Key: storepb.WorkspaceSettingKey_WORKSPACE_SETTING_SHORTCUT_RELATED,
					Value: &storepb.WorkspaceSetting_ShortcutRelated{
						ShortcutRelated: &storepb.WorkspaceSetting_ShortcutRelatedSetting{},
					},
				}
			}
			shortcutRelatedSetting.GetShortcutRelated().DefaultVisibility = convertVisibilityToStorepb(request.Setting.DefaultVisibility)
			if _, err := s.Store.UpsertWorkspaceSetting(ctx, &storepb.WorkspaceSetting{
				Key: storepb.WorkspaceSettingKey_WORKSPACE_SETTING_SHORTCUT_RELATED,
				Value: &storepb.WorkspaceSetting_ShortcutRelated{
					ShortcutRelated: shortcutRelatedSetting.GetShortcutRelated(),
				},
			}); err != nil {
				return nil, status.Errorf(codes.Internal, "failed to update workspace setting: %v", err)
			}
		} else if path == "identity_providers" {
			identityProviderSetting := &storepb.WorkspaceSetting_IdentityProviderSetting{}
			for _, identityProvider := range request.Setting.IdentityProviders {
				identityProviderSetting.IdentityProviders = append(identityProviderSetting.IdentityProviders, convertIdentityProviderToStore(identityProvider))
			}
			if _, err := s.Store.UpsertWorkspaceSetting(ctx, &storepb.WorkspaceSetting{
				Key: storepb.WorkspaceSettingKey_WORKSPACE_SETTING_IDENTITY_PROVIDER,
				Value: &storepb.WorkspaceSetting_IdentityProvider{
					IdentityProvider: identityProviderSetting,
				},
			}); err != nil {
				return nil, status.Errorf(codes.Internal, "failed to update workspace setting: %v", err)
			}
		} else if path == "disallow_user_registration" {
			securitySetting, err := s.Store.GetWorkspaceSecuritySetting(ctx)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "failed to get workspace setting: %v", err)
			}
			securitySetting.DisallowUserRegistration = request.Setting.DisallowUserRegistration
			if _, err := s.Store.UpsertWorkspaceSetting(ctx, &storepb.WorkspaceSetting{
				Key: storepb.WorkspaceSettingKey_WORKSPACE_SETTING_SECURITY,
				Value: &storepb.WorkspaceSetting_Security{
					Security: securitySetting,
				},
			}); err != nil {
				return nil, status.Errorf(codes.Internal, "failed to update workspace setting: %v", err)
			}
		} else if path == "disallow_password_auth" {
			securitySetting, err := s.Store.GetWorkspaceSecuritySetting(ctx)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "failed to get workspace setting: %v", err)
			}
			securitySetting.DisallowPasswordAuth = request.Setting.DisallowPasswordAuth
			if _, err := s.Store.UpsertWorkspaceSetting(ctx, &storepb.WorkspaceSetting{
				Key: storepb.WorkspaceSettingKey_WORKSPACE_SETTING_SECURITY,
				Value: &storepb.WorkspaceSetting_Security{
					Security: securitySetting,
				},
			}); err != nil {
				return nil, status.Errorf(codes.Internal, "failed to update workspace setting: %v", err)
			}
		} else {
			return nil, status.Errorf(codes.InvalidArgument, "invalid path: %s", path)
		}
	}

	workspaceSetting, err := s.GetWorkspaceSetting(ctx, &v1pb.GetWorkspaceSettingRequest{})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get workspace setting: %v", err)
	}
	return workspaceSetting, nil
}

var ownerCache *v1pb.User

func (s *APIV1Service) GetInstanceOwner(ctx context.Context) (*v1pb.User, error) {
	if ownerCache != nil {
		return ownerCache, nil
	}

	adminRole := store.RoleAdmin
	user, err := s.Store.GetUser(ctx, &store.FindUser{
		Role: &adminRole,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to find admin")
	}
	if user == nil {
		return nil, nil
	}

	ownerCache = convertUserFromStore(user)
	return ownerCache, nil
}

func convertIdentityProviderFromStore(identityProvider *storepb.IdentityProvider) *v1pb.IdentityProvider {
	if identityProvider == nil {
		return nil
	}
	return &v1pb.IdentityProvider{
		Id:     identityProvider.Id,
		Title:  identityProvider.Title,
		Type:   v1pb.IdentityProvider_Type(identityProvider.Type),
		Config: convertIdentityProviderConfigFromStore(identityProvider.Config),
	}
}

func convertIdentityProviderConfigFromStore(identityProviderConfig *storepb.IdentityProviderConfig) *v1pb.IdentityProviderConfig {
	oauth2Config := identityProviderConfig.GetOauth2()
	if oauth2Config != nil {
		return &v1pb.IdentityProviderConfig{
			Config: &v1pb.IdentityProviderConfig_Oauth2{
				Oauth2: &v1pb.IdentityProviderConfig_OAuth2Config{
					ClientId:     oauth2Config.ClientId,
					ClientSecret: oauth2Config.ClientSecret,
					AuthUrl:      oauth2Config.AuthUrl,
					TokenUrl:     oauth2Config.TokenUrl,
					UserInfoUrl:  oauth2Config.UserInfoUrl,
					Scopes:       oauth2Config.Scopes,
					FieldMapping: &v1pb.IdentityProviderConfig_FieldMapping{
						Identifier:  oauth2Config.FieldMapping.Identifier,
						DisplayName: oauth2Config.FieldMapping.DisplayName,
					},
				},
			},
		}
	}
	return nil
}

func convertIdentityProviderToStore(identityProvider *v1pb.IdentityProvider) *storepb.IdentityProvider {
	if identityProvider == nil {
		return nil
	}
	return &storepb.IdentityProvider{
		Id:     identityProvider.Id,
		Title:  identityProvider.Title,
		Type:   storepb.IdentityProvider_Type(identityProvider.Type),
		Config: convertIdentityProviderConfigToStore(identityProvider.Config),
	}
}

func convertIdentityProviderConfigToStore(identityProviderConfig *v1pb.IdentityProviderConfig) *storepb.IdentityProviderConfig {
	oauth2Config := identityProviderConfig.GetOauth2()
	if oauth2Config != nil {
		return &storepb.IdentityProviderConfig{
			Config: &storepb.IdentityProviderConfig_Oauth2{
				Oauth2: &storepb.IdentityProviderConfig_OAuth2Config{
					ClientId:     oauth2Config.ClientId,
					ClientSecret: oauth2Config.ClientSecret,
					AuthUrl:      oauth2Config.AuthUrl,
					TokenUrl:     oauth2Config.TokenUrl,
					UserInfoUrl:  oauth2Config.UserInfoUrl,
					Scopes:       oauth2Config.Scopes,
					FieldMapping: &storepb.IdentityProviderConfig_FieldMapping{
						Identifier:  oauth2Config.FieldMapping.Identifier,
						DisplayName: oauth2Config.FieldMapping.DisplayName,
					},
				},
			},
		}
	}
	return nil
}
