package query

import (
	"errors"
	"gorm.io/gorm"
)

func GetCachedUrl(migrationId int64, url string) (string, error) {
	u := UrlEntry
	bean, err := u.Where(u.MigrationId.Eq(migrationId), u.Url.Eq(url)).Take()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return bean.LocalName, nil
}
