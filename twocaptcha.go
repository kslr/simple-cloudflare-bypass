package main

import (
	"encoding/json"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/pkg/errors"
)

type CreateTaskReuqest struct {
	ClientKey string         `json:"clientKey"`
	Task      *TurnstileTask `json:"task"`
}

type CreateTaskResponse struct {
	ErrorId          int    `json:"errorId"`
	ErrorCode        string `json:"errorCode"`
	ErrorDescription string `json:"errorDescription"`
	TaskId           int    `json:"taskId"`
}

type GetTaskResultRequest struct {
	ClientKey string `json:"clientKey"`
	TaskId    int    `json:"taskId"`
}

type Solution struct {
	Token     string `json:"token"`
	UserAgent string `json:"userAgent"`
}

type GetTaskResultResponse struct {
	ErrorId          int       `json:"errorId"`
	ErrorCode        string    `json:"errorCode"`
	ErrorDescription string    `json:"errorDescription"`
	Status           string    `json:"status"`
	Solution         *Solution `json:"solution"`
	Cost             string    `json:"cost"`
	Ip               string    `json:"ip"`
	CreateTime       int       `json:"createTime"`
	EndTime          int       `json:"endTime"`
	SolveCount       int       `json:"solveCount"`
}

type TurnstileTask struct {
	Type       string `json:"type"`
	WebsiteURL string `json:"websiteURL"`
	WebsiteKey string `json:"websiteKey"`
	UserAgent  string `json:"userAgent"`
	Action     string `json:"action"`
	Data       string `json:"data"`
	PageData   string `json:"pagedata"`

	ProxyType     string `json:"proxyType"`
	ProxyAddress  string `json:"proxyAddress"`
	ProxyPort     string `json:"proxyPort"`
	ProxyLogin    string `json:"proxyLogin"`
	ProxyPassword string `json:"proxyPassword"`
}

func SolveCaptcha(task *TurnstileTask) (*GetTaskResultResponse, error) {
	createtaskRequestPayload, _ := json.Marshal(&CreateTaskReuqest{
		ClientKey: twoCaptchaApiKey,
		Task:      task,
	})

	createTaskResponse, err := resty.New().R().
		SetBody(createtaskRequestPayload).
		SetResult(&CreateTaskResponse{}).
		Post("https://api.2captcha.com/createTask")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to send createTask request")
	}
	if createTaskResponse.Result().(*CreateTaskResponse).ErrorId != 0 {
		return nil, errors.Errorf("createTask error: %s", createTaskResponse.Result().(*CreateTaskResponse).ErrorCode)
	}

	// per 5 seconds, max 12 times, timeout 60 seconds
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-time.After(60 * time.Second):
			return nil, errors.New("get captcha result timeout")
		case <-ticker.C:
			getTaskResultRequestPayload, _ := json.Marshal(&GetTaskResultRequest{
				ClientKey: twoCaptchaApiKey,
				TaskId:    createTaskResponse.Result().(*CreateTaskResponse).TaskId,
			})
			response, err := resty.New().
				SetRetryCount(3).
				SetRetryWaitTime(1 * time.Second).
				R().
				SetBody(getTaskResultRequestPayload).
				SetResult(&GetTaskResultResponse{}).
				Post("https://api.2captcha.com/getTaskResult")
			if err != nil {
				return nil, errors.Wrapf(err, "failed to send getTaskResult request")
			}

			result := response.Result().(*GetTaskResultResponse)
			if result.ErrorId == 0 {
				if result.Status == "processing" {
					continue
				}
				if result.Status == "ready" {
					return result, nil
				}
			} else {
				return nil, errors.Errorf("captcha result error: %d, code %s desc %s", result.ErrorId, result.ErrorCode, result.ErrorDescription)
			}
		}
	}

}
