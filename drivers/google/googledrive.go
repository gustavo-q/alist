package google

import (
	"fmt"
	"github.com/Xhofe/alist/conf"
	"github.com/Xhofe/alist/drivers/base"
	"github.com/Xhofe/alist/model"
	"github.com/Xhofe/alist/utils"
	"github.com/go-resty/resty/v2"
	log "github.com/sirupsen/logrus"
	"path/filepath"
	"strconv"
	"time"
)

type TokenError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (driver GoogleDrive) RefreshToken(account *model.Account) error {
	url := "https://www.googleapis.com/oauth2/v4/token"
	if account.APIProxyUrl != "" {
		url = fmt.Sprintf("%s/%s", account.APIProxyUrl, url)
	}
	var resp base.TokenResp
	var e TokenError
	res, err := base.RestyClient.R().SetResult(&resp).SetError(&e).
		SetFormData(map[string]string{
			"client_id":     account.ClientId,
			"client_secret": account.ClientSecret,
			"refresh_token": account.RefreshToken,
			"grant_type":    "refresh_token",
		}).Post(url)
	if err != nil {
		return err
	}
	log.Debug(res.String())
	if e.Error != "" {
		return fmt.Errorf(e.Error)
	}
	account.AccessToken = resp.AccessToken
	account.Status = "work"
	return nil
}

type File struct {
	Id           string     `json:"id"`
	Name         string     `json:"name"`
	MimeType     string     `json:"mimeType"`
	ModifiedTime *time.Time `json:"modifiedTime"`
	Size         string     `json:"size"`
}

func (driver GoogleDrive) IsDir(mimeType string) bool {
	return mimeType == "application/vnd.google-apps.folder" || mimeType == "application/vnd.google-apps.shortcut"
}

func (driver GoogleDrive) FormatFile(file *File) *model.File {
	f := &model.File{
		Id:        file.Id,
		Name:      file.Name,
		Driver:    driver.Config().Name,
		UpdatedAt: file.ModifiedTime,
		Thumbnail: "",
		Url:       "",
	}
	if driver.IsDir(file.MimeType) {
		f.Type = conf.FOLDER
	} else {
		size, _ := strconv.ParseInt(file.Size, 10, 64)
		f.Size = size
		f.Type = utils.GetFileType(filepath.Ext(file.Name))
	}
	return f
}

type Files struct {
	NextPageToken string `json:"nextPageToken"`
	Files         []File `json:"files"`
}

type Error struct {
	Error struct {
		Errors []struct {
			Domain       string `json:"domain"`
			Reason       string `json:"reason"`
			Message      string `json:"message"`
			LocationType string `json:"location_type"`
			Location     string `json:"location"`
		}
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (driver GoogleDrive) GetFiles(id string, account *model.Account) ([]File, error) {
	pageToken := "first"
	res := make([]File, 0)
	for pageToken != "" {
		if pageToken == "first" {
			pageToken = ""
		}
		var resp Files
		query := map[string]string{
			"orderBy":                   "folder,name,modifiedTime desc",
			"fields":                    "files(id,name,mimeType,size,modifiedTime),nextPageToken",
			"pageSize":                  "1000",
			"q":                         fmt.Sprintf("'%s' in parents and trashed = false", id),
			"includeItemsFromAllDrives": "true",
			"supportsAllDrives":         "true",
			"pageToken":                 pageToken,
		}
		_, err := driver.Request("https://www.googleapis.com/drive/v3/files",
			base.Get, nil, query, nil, nil, &resp, account)
		if err != nil {
			return nil, err
		}
		pageToken = resp.NextPageToken
		res = append(res, resp.Files...)
	}
	return res, nil
}

func (driver GoogleDrive) Request(url string, method int, headers, query, form map[string]string, data *base.Json, resp interface{}, account *model.Account) ([]byte, error) {
	rawUrl := url
	if account.APIProxyUrl != "" {
		url = fmt.Sprintf("%s/%s", account.APIProxyUrl, url)
	}
	req := base.RestyClient.R()
	req.SetHeader("Authorization", "Bearer "+account.AccessToken)
	if headers != nil {
		req.SetHeaders(headers)
	}
	if query != nil {
		req.SetQueryParams(query)
	}
	if form != nil {
		req.SetFormData(form)
	}
	if data != nil {
		req.SetBody(data)
	}
	if resp != nil {
		req.SetResult(resp)
	}
	var res *resty.Response
	var err error
	var e Error
	req.SetError(&e)
	switch method {
	case base.Get:
		res, err = req.Get(url)
	case base.Post:
		res, err = req.Post(url)
	default:
		return nil, base.ErrNotSupport
	}
	if err != nil {
		return nil, err
	}
	log.Debug(res.String())
	if e.Error.Code != 0 {
		if e.Error.Code == 401 {
			err = driver.RefreshToken(account)
			if err != nil {
				_ = model.SaveAccount(account)
				return nil, err
			}
			return driver.Request(rawUrl, method, headers, query, form, data, resp, account)
		}
		return nil, fmt.Errorf("%s: %v", e.Error.Message, e.Error.Errors)
	}
	return res.Body(), nil
}

func init() {
	base.RegisterDriver(&GoogleDrive{})
}
