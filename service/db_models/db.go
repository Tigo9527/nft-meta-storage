package db_models

import (
	"time"
)

var MigrationModels = []interface{}{
	&FileEntry{},
	&User{},
	&RootIndex{},
	&FileTxQueue{},
	&FileStoreQueue{},

	&Migration{},
	&Config{},
}

const ConfigDownloadingID = "downloading"
const ConfigImageGateway = "imageGateway"

type Config struct {
	Name  string `json:"name" gorm:"primary_key"`
	Value string `json:"value" gorm:"type: varchar(255) null"`
}

const MigrationStatusDownload = "download"
const MigrationStatusPackImage = "packImage"
const MigrationStatusPackMeta = "packMeta"
const MigrationStatusUploadImage = "uploadImage"
const MigrationStatusWaitUploadingImage = "waitUploadingImage"
const MigrationStatusUploadMeta = "uploadMeta"
const MigrationStatusWaitUploadingMeta = "waitUploadingMeta"
const MigrationStatusFinished = "finished"

type Migration struct {
	Id               int64      `json:"id" gorm:"primary_key"`
	Addr             string     `json:"addr" binding:"required" gorm:"type: varchar(128) not null unique"`
	ChainRpc         string     `json:"chainRpc" binding:"" gorm:"type: varchar(300) not null"`
	TotalSupply      int        `json:"totalSupply" gorm:""`
	DownloadedMeta   int        `json:"downloadedMeta" gorm:""`
	Status           string     `json:"status" binding:"" gorm:"type: varchar(64) not null"`
	ImageFileEntryId int        `json:"imageFileEntryId" gorm:""`
	MetaFileEntryId  int        `json:"metaFileEntryId" gorm:""`
	ImageUploaded    bool       `json:"imageUploaded" binding:"" gorm:"type: bool not null"`
	MetaUploaded     bool       `json:"metaUploaded" binding:"" gorm:"type: bool not null"`
	Name             string     `json:"name" binding:"required" gorm:"type: varchar(128) not null"`
	CreatedAt        *time.Time `json:"created_at,string,omitempty"`
	UpdatedAt        *time.Time `json:"updated_at,string,omitempty"`
}

type FileEntry struct {
	Id        int64      `json:"id" gorm:"primary_key"`
	UserId    int64      `json:"user_id" gorm:"index"`
	Name      string     `json:"name" binding:"required" gorm:"not null"`
	Size      int64      `json:"size" gorm:""`
	RootId    int64      `json:"root_id" gorm:""`
	CreatedAt *time.Time `json:"created_at,string,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,string,omitempty"`
}

// RootIndex unique FileRoot
type RootIndex struct {
	// it's file entry id
	Id         int64      `json:"id" gorm:"primary_key"`
	FileId     int64      `json:"file_id" gorm:"not null"`
	Root       string     `json:"root" binding:"" gorm:"type:char(66) unique not null"`
	TxHash     string     `json:"tx_hash" binding:"" gorm:"type:char(66)"`
	PaidAt     *time.Time `json:"paid_at,string,omitempty"`
	UploadedAt *time.Time `json:"uploaded_at,string,omitempty"`
	CreatedAt  *time.Time `json:"created_at,string,omitempty"`
	UpdatedAt  *time.Time `json:"updated_at,string,omitempty"`
}

// FileTxQueue each record represents a payment task that have not been finished on layer 1
type FileTxQueue struct {
	Id        int64      `json:"id" gorm:"primary_key"`
	FileId    int64      `json:"file_id" gorm:"not null"`
	CreatedAt *time.Time `json:"created_at,string,omitempty"`
}

const UploadStepUploading = "uploading"
const UploadStepWaitConfirm = "waitConfirm"

// FileStoreQueue each record represents an uploading task that have not been finished on storage node
type FileStoreQueue struct {
	Id        int64      `json:"id" gorm:"primary_key"`
	RootId    int64      `json:"root_id" gorm:"not null"`
	Step      string     `json:"step" gorm:"type:varchar(32) not null"`
	CreatedAt *time.Time `json:"created_at,string,omitempty"`
}

type User struct {
	Id        int64      `json:"id" gorm:"primary_key"`
	Name      string     `json:"name" binding:"required" gorm:"unique,not null"`
	Token     string     `json:"token" binding:"required" gorm:"type:varchar(66) unique not null"`
	CreatedAt *time.Time `json:"created_at,string,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,string,omitempty"`
}
