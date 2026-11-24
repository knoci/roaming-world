package service

import (
	"context"
	"errors"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/knoci/roaming-world/pkg/auth"
	pb "github.com/knoci/roaming-world/plan/api/plan/v1"
	"github.com/knoci/roaming-world/plan/internal/biz"
)

type PlanService struct {
	pb.UnimplementedPlanServer
	uc  *biz.PlanUsecase
	log *log.Helper
}

func NewPlanService(uc *biz.PlanUsecase, logger log.Logger) *PlanService {
	return &PlanService{uc: uc, log: log.NewHelper(logger)}
}

// ReadyCheck verifies underlying PostgreSQL connectivity for /readyz probes.
func (s *PlanService) ReadyCheck(ctx context.Context) error {
	if s == nil || s.uc == nil {
		return errors.New("plan service is not initialized")
	}
	return s.uc.Ping(ctx)
}

func (s *PlanService) CreatePlan(ctx context.Context, req *pb.CreatePlanRequest) (*pb.CreatePlanReply, error) {
	claims, err := getJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	plan, err := s.uc.Create(ctx, claims.UID, req.Title, req.City, req.StartDate, req.EndDate, req.Visibility, req.Cover, int(req.Budget))
	if err != nil {
		return nil, err
	}
	return &pb.CreatePlanReply{Plan: toMessage(plan)}, nil
}

func (s *PlanService) UpdatePlan(ctx context.Context, req *pb.UpdatePlanRequest) (*pb.UpdatePlanReply, error) {
	claims, err := getJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	plan, err := s.uc.Update(ctx, claims.UID, req.Id, req.Title, req.City, req.Visibility, req.Cover, int(req.Budget))
	if err != nil {
		return nil, err
	}
	return &pb.UpdatePlanReply{Plan: toMessage(plan)}, nil
}

func (s *PlanService) GetPlan(ctx context.Context, req *pb.GetPlanRequest) (*pb.GetPlanReply, error) {
	claims, err := getOptionalJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	uid := ""
	if claims != nil {
		uid = claims.UID
	}
	plan, err := s.uc.Get(ctx, req.Id, uid)
	if err != nil {
		return nil, err
	}
	return &pb.GetPlanReply{Plan: toMessage(plan)}, nil
}

func (s *PlanService) ListPlans(ctx context.Context, req *pb.ListPlansRequest) (*pb.ListPlansReply, error) {
	claims, err := getOptionalJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	uid := ""
	if claims != nil {
		uid = claims.UID
	}
	plans, total, err := s.uc.List(ctx, uid, req.Scope, int(req.Page), int(req.Limit), req.SummaryOnly)
	if err != nil {
		return nil, err
	}
	reply := &pb.ListPlansReply{Total: total, Plans: make([]*pb.PlanMessage, 0, len(plans))}
	for _, plan := range plans {
		reply.Plans = append(reply.Plans, toMessage(plan))
	}
	return reply, nil
}

func (s *PlanService) PublishPlan(ctx context.Context, req *pb.PublishPlanRequest) (*pb.PublishPlanReply, error) {
	claims, err := getJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	status, err := s.uc.Publish(ctx, claims.UID, req.Id)
	if err != nil {
		return nil, err
	}
	return &pb.PublishPlanReply{Status: status}, nil
}

func (s *PlanService) AddPlanItem(ctx context.Context, req *pb.AddPlanItemRequest) (*pb.AddPlanItemReply, error) {
	claims, err := getJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	plan, err := s.uc.AddItem(ctx, claims.UID, req.Id, int(req.DayNo), req.ItemType, req.ItemId, req.StartTime, req.EndTime, req.Note)
	if err != nil {
		return nil, err
	}
	return &pb.AddPlanItemReply{Plan: toMessage(plan)}, nil
}

func (s *PlanService) DeletePlanItem(ctx context.Context, req *pb.DeletePlanItemRequest) (*pb.DeletePlanItemReply, error) {
	claims, err := getJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	if err := s.uc.DeleteItem(ctx, claims.UID, req.Id, req.ItemId); err != nil {
		return nil, err
	}
	return &pb.DeletePlanItemReply{Status: "deleted"}, nil
}

func (s *PlanService) DeletePlan(ctx context.Context, req *pb.DeletePlanRequest) (*pb.DeletePlanReply, error) {
	claims, err := getJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	if err := s.uc.Delete(ctx, claims.UID, req.Id); err != nil {
		return nil, err
	}
	return &pb.DeletePlanReply{Status: "deleted"}, nil
}

func getOptionalJwtClaim(ctx context.Context) (*auth.Claims, error) {
	token, present := auth.ExtractBearerToken(ctx)
	if !present {
		return nil, nil
	}
	if token == "" {
		return nil, biz.ErrUnauthorized
	}
	claims, err := auth.ParseClaimsFromContext(ctx, nil)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	return claims, nil
}

func getJwtClaim(ctx context.Context) (*auth.Claims, error) {
	claims, err := auth.ParseClaimsFromContext(ctx, nil)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	return claims, nil
}

func toMessage(plan *biz.Plan) *pb.PlanMessage {
	if plan == nil {
		return nil
	}
	msg := &pb.PlanMessage{
		Id:         plan.ID,
		Uid:        plan.UID,
		Title:      plan.Title,
		City:       plan.City,
		StartDate:  plan.StartDate.Format("2006-01-02"),
		EndDate:    plan.EndDate.Format("2006-01-02"),
		Budget:     int32(plan.Budget),
		Visibility: plan.Visibility,
		Status:     plan.Status,
		Cover:      plan.Cover,
		CreatedAt:  plan.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  plan.UpdatedAt.Format(time.RFC3339),
	}
	for _, d := range plan.Days {
		day := &pb.PlanDayMessage{DayNo: int32(d.DayNo), Date: d.Date.Format("2006-01-02"), Title: d.Title}
		for _, it := range d.Items {
			day.Items = append(day.Items, &pb.PlanItemMessage{
				Id: it.ID, DayNo: int32(d.DayNo), ItemType: it.ItemType, ItemId: it.ItemID,
				StartTime: it.StartTime, EndTime: it.EndTime, Note: it.Note,
			})
		}
		msg.Days = append(msg.Days, day)
	}
	return msg
}
