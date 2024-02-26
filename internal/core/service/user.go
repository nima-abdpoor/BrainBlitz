package service

import (
	utils "BrainBlitz.com/game/internal/core/common"
	"BrainBlitz.com/game/internal/core/dto"
	"BrainBlitz.com/game/internal/core/entity/error_code"
	"BrainBlitz.com/game/internal/core/model/request"
	"BrainBlitz.com/game/internal/core/model/response"
	"BrainBlitz.com/game/internal/core/port/repository"
	"BrainBlitz.com/game/internal/core/port/service"
	"BrainBlitz.com/game/pkg/email"
	"strings"
)

const (
	invalidUserNameErrMsg = "invalid username"
	invalidPasswordErrMsg = "invalid password"
)

type UserService struct {
	userRepo repository.UserRepository
}

func NewUserService(userRepo repository.UserRepository) service.UserService {
	return &UserService{
		userRepo: userRepo,
	}
}

func (us UserService) SignUp(request *request.SignUpRequest) *response.Response {
	//validate request
	if !email.IsValid(request.Email) {
		return us.createFailedResponse(error_code.BadRequest, invalidUserNameErrMsg)
	}

	if len(request.Password) == 0 {
		return us.createFailedResponse(error_code.BadRequest, invalidPasswordErrMsg)
	}

	currentTime := utils.GetUTCCurrentMillis()

	hashPassword, err := utils.HashPassword(request.Password)
	if err != nil {
		return us.createFailedResponse(error_code.InternalError, error_code.GetLocalErrorCode(error_code.BcryptErrorHashingPassword))
	}

	userDto := dto.UserDTO{
		Email:          request.Email,
		HashedPassword: hashPassword,
		DisplayName:    getDisplayName(request.Email),
		CreatedAt:      currentTime,
		UpdatedAt:      currentTime,
	}

	//save a new user
	err = us.userRepo.InsertUser(userDto)
	if err != nil {
		if err == repository.DuplicateUser {
			return us.createFailedResponse(error_code.BadRequest, err.Error())
		}
		return us.createFailedResponse(error_code.InternalError, error_code.InternalErrMsg)
	}

	// create data response
	signUpData := response.SignUpDataResponse{
		DisplayName: userDto.DisplayName,
	}
	return us.createSuccessResponse(signUpData)
}

func getDisplayName(email string) string {
	return strings.Split(email, "@")[0]
}

func (us UserService) createFailedResponse(errorCode int, message string) *response.Response {
	return &response.Response{
		Status:       false,
		ErrorCode:    errorCode,
		ErrorMessage: message,
	}
}

func (us UserService) createSuccessResponse(data response.SignUpDataResponse) *response.Response {
	return &response.Response{
		Data:         data,
		Status:       true,
		ErrorCode:    error_code.Success,
		ErrorMessage: error_code.SuccessErrMsg,
	}
}
