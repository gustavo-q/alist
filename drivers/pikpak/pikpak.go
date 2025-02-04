package pikpak

import (
	"errors"
	"fmt"
	"github.com/Xhofe/alist/conf"
	"github.com/Xhofe/alist/drivers/base"
	"github.com/Xhofe/alist/model"
	"github.com/Xhofe/alist/utils"
	"github.com/go-resty/resty/v2"
	jsoniter "github.com/json-iterator/go"
	log "github.com/sirupsen/logrus"
	"path/filepath"
	"strconv"
	"time"
)

type RespErr struct {
	ErrorCode int    `json:"error_code"`
	Error     string `json:"error"`
}

func (driver PikPak) Login(account *model.Account) error {
	url := "https://user.mypikpak.com/v1/auth/signin"
	if account.APIProxyUrl != "" {
		url = fmt.Sprintf("%s/%s", account.APIProxyUrl, url)
	}
	var e RespErr
	res, err := base.RestyClient.R().SetError(&e).SetBody(base.Json{
		"captcha_token": "",
		"client_id":     "YNxT9w7GMdWvEOKa",
		"client_secret": "dbw2OtmVEeuUvIptb1Coyg",
		"username":      account.Username,
		"password":      account.Password,
	}).Post(url)
	if err != nil {
		account.Status = err.Error()
		return err
	}
	log.Debug(res.String())
	if e.ErrorCode != 0 {
		account.Status = e.Error
		return errors.New(e.Error)
	}
	data := res.Body()
	account.Status = "work"
	account.RefreshToken = jsoniter.Get(data, "refresh_token").ToString()
	account.AccessToken = jsoniter.Get(data, "access_token").ToString()
	_ = model.SaveAccount(account)
	return nil
}

func (driver PikPak) RefreshToken(account *model.Account) error {
	url := "https://user.mypikpak.com/v1/auth/token"
	if account.APIProxyUrl != "" {
		url = fmt.Sprintf("%s/%s", account.APIProxyUrl, url)
	}
	var e RespErr
	res, err := base.RestyClient.R().SetError(&e).
		SetHeader("user-agent", "").SetBody(base.Json{
		"client_id":     "YNxT9w7GMdWvEOKa",
		"client_secret": "dbw2OtmVEeuUvIptb1Coyg",
		"grant_type":    "refresh_token",
		"refresh_token": account.RefreshToken,
	}).Post(url)
	if err != nil {
		account.Status = err.Error()
		return err
	}
	if e.ErrorCode != 0 {
		if e.ErrorCode == 4126 {
			// refresh_token 失效，重新登陆
			return driver.Login(account)
		}
		return errors.New(e.Error)
	}
	data := res.Body()
	account.Status = "work"
	account.RefreshToken = jsoniter.Get(data, "refresh_token").ToString()
	account.AccessToken = jsoniter.Get(data, "access_token").ToString()
	log.Debugf("%s\n %+v", res.String(), account)
	_ = model.SaveAccount(account)
	return nil
}

func (driver PikPak) Request(url string, method int, query map[string]string, data *base.Json, resp interface{}, account *model.Account) ([]byte, error) {
	rawUrl := url
	if account.APIProxyUrl != "" {
		url = fmt.Sprintf("%s/%s", account.APIProxyUrl, url)
	}
	req := base.RestyClient.R()
	req.SetHeader("Authorization", "Bearer "+account.AccessToken)
	if query != nil {
		req.SetQueryParams(query)
	}
	if data != nil {
		req.SetBody(data)
	}
	if resp != nil {
		req.SetResult(resp)
	}
	var e RespErr
	req.SetError(&e)
	var res *resty.Response
	var err error
	switch method {
	case base.Get:
		res, err = req.Get(url)
	case base.Post:
		res, err = req.Post(url)
	case base.Patch:
		res, err = req.Patch(url)
	default:
		return nil, base.ErrNotSupport
	}
	if err != nil {
		return nil, err
	}
	log.Debug(res.String())
	if e.ErrorCode != 0 {
		if e.ErrorCode == 16 {
			// login / refresh token
			err = driver.RefreshToken(account)
			if err != nil {
				return nil, err
			}
			return driver.Request(rawUrl, method, query, data, resp, account)
		} else {
			return nil, errors.New(e.Error)
		}
	}
	return res.Body(), nil
}

type File struct {
	Id             string     `json:"id"`
	Kind           string     `json:"kind"`
	Name           string     `json:"name"`
	ModifiedTime   *time.Time `json:"modified_time"`
	Size           string     `json:"size"`
	ThumbnailLink  string     `json:"thumbnail_link"`
	WebContentLink string     `json:"web_content_link"`
}

func (driver PikPak) FormatFile(file *File) *model.File {
	size, _ := strconv.ParseInt(file.Size, 10, 64)
	f := &model.File{
		Id:        file.Id,
		Name:      file.Name,
		Size:      size,
		Driver:    driver.Config().Name,
		UpdatedAt: file.ModifiedTime,
		Thumbnail: file.ThumbnailLink,
	}
	if file.Kind == "drive#folder" {
		f.Type = conf.FOLDER
	} else {
		f.Type = utils.GetFileType(filepath.Ext(file.Name))
	}
	return f
}

type Files struct {
	Files         []File `json:"files"`
	NextPageToken string `json:"next_page_token"`
}

func (driver PikPak) GetFiles(id string, account *model.Account) ([]File, error) {
	res := make([]File, 0)
	pageToken := "first"
	for pageToken != "" {
		if pageToken == "first" {
			pageToken = ""
		}
		query := map[string]string{
			"parent_id":      id,
			"thumbnail_size": "SIZE_LARGE",
			"with_audit":     "true",
			"limit":          "100",
			"filters":        `{"phase":{"eq":"PHASE_TYPE_COMPLETE"},"trashed":{"eq":false}}`,
			"page_token":     pageToken,
		}
		var resp Files
		_, err := driver.Request("https://api-drive.mypikpak.com/drive/v1/files", base.Get, query, nil, &resp, account)
		if err != nil {
			return nil, err
		}
		log.Debugf("%+v", resp)
		pageToken = resp.NextPageToken
		res = append(res, resp.Files...)
	}
	return res, nil
}

func init() {
	base.RegisterDriver(&PikPak{})
}
