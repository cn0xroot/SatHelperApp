package Structs

import "github.com/opensatelliteproject/SatHelperApp/XRIT/PacketData"

type StringFieldRecord struct {
	Type       byte
	StringData string
}

type AncillaryText StringFieldRecord
type AnnotationRecord StringFieldRecord
type DCSFilenameRecord StringFieldRecord
type HeaderStructuredRecord StringFieldRecord
type ImageDataFunctionRecord StringFieldRecord

func MakeAncillaryText(data []byte) *AncillaryText {
	v := AncillaryText{}

	v.Type = PacketData.AncillaryTextRecord

	v.StringData = string(data)

	return &v
}
func MakeAnnotationRecord(data []byte) *AnnotationRecord {
	v := AnnotationRecord{}

	v.Type = PacketData.AnnotationRecord

	v.StringData = string(data)

	return &v
}
func MakeDCSFilenameRecord(data []byte) *DCSFilenameRecord {
	v := DCSFilenameRecord{}

	v.Type = PacketData.DCSFileNameRecord

	v.StringData = string(data)

	return &v
}
func MakeHeaderStructuredRecord(data []byte) *HeaderStructuredRecord {
	v := HeaderStructuredRecord{}

	v.Type = PacketData.HeaderStructuredRecord

	v.StringData = string(data)

	return &v
}
func MakeImageDataFunctionRecord(data []byte) *ImageDataFunctionRecord {
	v := ImageDataFunctionRecord{}

	v.Type = PacketData.ImageDataFunctionRecord

	v.StringData = string(data)

	return &v
}

func (sfr *StringFieldRecord) GetType() int {
	return int(sfr.Type)
}
func (a *AncillaryText) GetType() int {
	return int(a.Type)
}
func (a *AnnotationRecord) GetType() int {
	return int(a.Type)
}
func (a *DCSFilenameRecord) GetType() int {
	return int(a.Type)
}
func (a *HeaderStructuredRecord) GetType() int {
	return int(a.Type)
}
func (a *ImageDataFunctionRecord) GetType() int {
	return int(a.Type)
}
