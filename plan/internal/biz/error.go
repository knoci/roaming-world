package biz

import "github.com/go-kratos/kratos/v2/errors"

var (
	ErrInvalidArgument   = errors.BadRequest("INVALID_ARGUMENT", "invalid argument")
	ErrUnauthorized      = errors.Unauthorized("UNAUTHORIZED", "unauthorized")
	ErrNoPermission      = errors.Forbidden("NO_PERMISSION", "no permission")
	ErrPlanNotFound      = errors.NotFound("PLAN_NOT_FOUND", "plan not found")
	ErrDayNotFound       = errors.NotFound("DAY_NOT_FOUND", "plan day not found")
	ErrInvalidDate       = errors.BadRequest("INVALID_DATE", "invalid date, use YYYY-MM-DD")
	ErrInvalidDateRange  = errors.BadRequest("INVALID_DATE_RANGE", "end date must be after start date")
	ErrPlanTooLong       = errors.BadRequest("PLAN_TOO_LONG", "plan cannot exceed 30 days")
	ErrInvalidVisibility = errors.BadRequest("INVALID_VISIBILITY", "visibility must be private or public")
	ErrPlanNotEditable   = errors.BadRequest("PLAN_NOT_EDITABLE", "only draft plans can be edited")
	ErrEmptyPlan         = errors.BadRequest("EMPTY_PLAN", "plan needs at least one item before publishing")
	ErrInvalidTime       = errors.BadRequest("INVALID_TIME", "start/end time must be HH:MM and start < end")
	ErrTimeOverlap       = errors.BadRequest("TIME_OVERLAP", "plan items overlap")
	ErrCreateFailed      = errors.InternalServer("CREATE_PLAN_FAILED", "create plan failed")
	ErrUpdateFailed      = errors.InternalServer("UPDATE_PLAN_FAILED", "update plan failed")
)
