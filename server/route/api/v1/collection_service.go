package v1

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	v1pb "github.com/warthurton/slash/proto/gen/api/v1"
	storepb "github.com/warthurton/slash/proto/gen/store"
	"github.com/warthurton/slash/server/service/license"
	"github.com/warthurton/slash/store"
)

func (s *APIV1Service) ListCollections(ctx context.Context, _ *v1pb.ListCollectionsRequest) (*v1pb.ListCollectionsResponse, error) {
	collections, err := s.Store.ListCollections(ctx, &store.FindCollection{})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get collection list, err: %v", err)
	}

	convertedCollections := []*v1pb.Collection{}
	for _, collection := range collections {
		convertedCollections = append(convertedCollections, convertCollectionFromStore(collection))
	}

	response := &v1pb.ListCollectionsResponse{
		Collections: convertedCollections,
	}
	return response, nil
}

func (s *APIV1Service) GetCollection(ctx context.Context, request *v1pb.GetCollectionRequest) (*v1pb.Collection, error) {
	collection, err := s.Store.GetCollection(ctx, &store.FindCollection{
		ID: &request.Id,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get collection by name: %v", err)
	}
	if collection == nil {
		return nil, status.Errorf(codes.NotFound, "collection not found")
	}

	user, err := getCurrentUser(ctx, s.Store)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get current user: %v", err)
	}
	if user == nil && collection.Visibility != storepb.Visibility_PUBLIC {
		return nil, status.Errorf(codes.PermissionDenied, "Permission denied")
	}
	return convertCollectionFromStore(collection), nil
}

func (s *APIV1Service) GetCollectionByName(ctx context.Context, request *v1pb.GetCollectionByNameRequest) (*v1pb.Collection, error) {
	collection, err := s.Store.GetCollection(ctx, &store.FindCollection{
		Name: &request.Name,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get collection by name: %v", err)
	}
	if collection == nil {
		return nil, status.Errorf(codes.NotFound, "collection not found")
	}

	user, err := getCurrentUser(ctx, s.Store)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get current user: %v", err)
	}
	if user == nil && collection.Visibility != storepb.Visibility_PUBLIC {
		return nil, status.Errorf(codes.PermissionDenied, "Permission denied")
	}
	return convertCollectionFromStore(collection), nil
}

func (s *APIV1Service) CreateCollection(ctx context.Context, request *v1pb.CreateCollectionRequest) (*v1pb.Collection, error) {
	if request.Collection.Name == "" || request.Collection.Title == "" {
		return nil, status.Errorf(codes.InvalidArgument, "name and title are required")
	}

	if !s.LicenseService.IsFeatureEnabled(license.FeatureTypeUnlimitedCollections) {
		collections, err := s.Store.ListCollections(ctx, &store.FindCollection{})
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to get collection list, err: %v", err)
		}
		collectionsLimit := int(s.LicenseService.GetSubscription().CollectionsLimit)
		if len(collections) >= collectionsLimit {
			return nil, status.Errorf(codes.PermissionDenied, "Maximum number of collections %d reached", collectionsLimit)
		}
	}

	user, err := getCurrentUser(ctx, s.Store)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get current user: %v", err)
	}
	collectionCreate := &storepb.Collection{
		CreatorId:   user.ID,
		Name:        request.Collection.Name,
		Title:       request.Collection.Title,
		Description: request.Collection.Description,
		ShortcutIds: request.Collection.ShortcutIds,
		Visibility:  convertVisibilityToStorepb(request.Collection.Visibility),
	}
	collection, err := s.Store.CreateCollection(ctx, collectionCreate)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create collection, err: %v", err)
	}

	return convertCollectionFromStore(collection), nil
}

func (s *APIV1Service) UpdateCollection(ctx context.Context, request *v1pb.UpdateCollectionRequest) (*v1pb.Collection, error) {
	if request.UpdateMask == nil || len(request.UpdateMask.Paths) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "updateMask is required")
	}

	user, err := getCurrentUser(ctx, s.Store)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get current user: %v", err)
	}
	collection, err := s.Store.GetCollection(ctx, &store.FindCollection{
		ID: &request.Collection.Id,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get collection by name: %v", err)
	}
	if collection == nil {
		return nil, status.Errorf(codes.NotFound, "collection not found")
	}
	if collection.CreatorId != user.ID && user.Role != store.RoleAdmin {
		return nil, status.Errorf(codes.PermissionDenied, "Permission denied")
	}

	update := &store.UpdateCollection{
		ID: collection.Id,
	}
	for _, path := range request.UpdateMask.Paths {
		switch path {
		case "name":
			update.Name = &request.Collection.Name
		case "title":
			update.Title = &request.Collection.Title
		case "description":
			update.Description = &request.Collection.Description
		case "shortcut_ids":
			update.ShortcutIDs = request.Collection.ShortcutIds
		case "visibility":
			visibility := convertVisibilityToStorepb(request.Collection.Visibility)
			update.Visibility = &visibility
		}
	}
	collection, err = s.Store.UpdateCollection(ctx, update)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update collection, err: %v", err)
	}

	return convertCollectionFromStore(collection), nil
}

func (s *APIV1Service) DeleteCollection(ctx context.Context, request *v1pb.DeleteCollectionRequest) (*emptypb.Empty, error) {
	user, err := getCurrentUser(ctx, s.Store)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get current user: %v", err)
	}
	collection, err := s.Store.GetCollection(ctx, &store.FindCollection{
		ID: &request.Id,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get collection by name: %v", err)
	}
	if collection == nil {
		return nil, status.Errorf(codes.NotFound, "collection not found")
	}
	if collection.CreatorId != user.ID && user.Role != store.RoleAdmin {
		return nil, status.Errorf(codes.PermissionDenied, "Permission denied")
	}

	err = s.Store.DeleteCollection(ctx, &store.DeleteCollection{
		ID: collection.Id,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete collection, err: %v", err)
	}
	return &emptypb.Empty{}, nil
}

func convertCollectionFromStore(collection *storepb.Collection) *v1pb.Collection {
	return &v1pb.Collection{
		Id:          collection.Id,
		CreatorId:   collection.CreatorId,
		CreatedTime: timestamppb.New(time.Unix(collection.CreatedTs, 0)),
		UpdatedTime: timestamppb.New(time.Unix(collection.UpdatedTs, 0)),
		Name:        collection.Name,
		Title:       collection.Title,
		Description: collection.Description,
		ShortcutIds: collection.ShortcutIds,
		Visibility:  convertVisibilityFromStorepb(collection.Visibility),
	}
}
