// Package store: akses data (SQL via pgx) + model JSON.
//
// Nama field JSON sengaja identik dengan output Prisma di backend lama
// (camelCase sesuai kolom), supaya FE tidak perlu berubah.
package store

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// Time diserialisasi seperti Date JS: ISO-8601 UTC dengan milidetik
// (2026-11-10T08:15:00.000Z).
type Time struct{ time.Time }

const jsTimeLayout = "2006-01-02T15:04:05.000Z"

func (t Time) MarshalJSON() ([]byte, error) {
	return []byte(`"` + t.UTC().Format(jsTimeLayout) + `"`), nil
}

func (t Time) String() string { return t.UTC().Format(jsTimeLayout) }

// ScanTimestamp: pgx scan langsung dari kolom timestamp.
func (t *Time) ScanTimestamp(v pgtype.Timestamp) error {
	t.Time = v.Time
	return nil
}

// Now = waktu sekarang dibulatkan ke milidetik (presisi kolom TIMESTAMP(3)).
func Now() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }

// Role & status (enum Postgres).
const (
	RoleCustomer  = "CUSTOMER"
	RoleCollector = "COLLECTOR"
	RoleAdmin     = "ADMIN"

	StatusPending              = "PENDING"
	StatusConfirmed            = "CONFIRMED"
	StatusOnTheWay             = "ON_THE_WAY"
	StatusInProgress           = "IN_PROGRESS"
	StatusAwaitingConfirmation = "AWAITING_CONFIRMATION"
	StatusCompleted            = "COMPLETED"
	StatusCancelled            = "CANCELLED"
)

// Me = profil user sendiri (GET/PATCH /auth/me).
type Me struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Email       string   `json:"email"`
	Role        string   `json:"role"`
	AvgRating   float64  `json:"avgRating"`
	AvatarURL   *string  `json:"avatarUrl"`
	Phone       *string  `json:"phone"`
	CreatedAt   Time     `json:"createdAt"`
	IsVerified  bool     `json:"isVerified"`
	NIK         *string  `json:"nik"`
	KTPName     *string  `json:"ktpName"`
	AddressText *string  `json:"addressText"`
	Lat         *float64 `json:"lat"`
	Lng         *float64 `json:"lng"`
}

// PublicUser = field user yang aman dikirim ke lawan transaksi (tanpa
// passwordHash, NIK, token reset, dsb).
type PublicUser struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Phone       *string `json:"phone"`
	AvatarURL   *string `json:"avatarUrl"`
	AvgRating   float64 `json:"avgRating"`
	IsVerified  bool    `json:"isVerified"`
	AddressText *string `json:"addressText"`
	Role        string  `json:"role"`
}

type Category struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	IconURL     *string `json:"iconUrl"`
	Unit        string  `json:"unit"`
	SortOrder   int     `json:"sortOrder"`
	ParentID    *string `json:"parentId"`
}

type CollectorProfile struct {
	ID                  string  `json:"id"`
	UserID              string  `json:"userId"`
	ShopName            string  `json:"shopName"`
	Description         *string `json:"description"`
	ShopImageURL        *string `json:"shopImageUrl"`
	RadiusKm            float64 `json:"radiusKm"`
	IsOpen              bool    `json:"isOpen"`
	OperatingHours      *string `json:"operatingHours"`
	IsPremium           bool    `json:"isPremium"`
	PriorityScore       int     `json:"priorityScore"`
	MaxConcurrentOrders int     `json:"maxConcurrentOrders"`
}

type Catalog struct {
	ID          string    `json:"id"`
	CollectorID string    `json:"collectorId"`
	CategoryID  string    `json:"categoryId"`
	MinPrice    float64   `json:"minPrice"`
	MaxPrice    float64   `json:"maxPrice"`
	IsActive    bool      `json:"isActive"`
	Category    *Category `json:"category,omitempty"`
}

type Order struct {
	ID                  string   `json:"id"`
	CustomerID          string   `json:"customerId"`
	CollectorID         *string  `json:"collectorId"`
	CategoryID          *string  `json:"categoryId"`
	Method              string   `json:"method"`
	PhotoURL            *string  `json:"photoUrl"`
	AddressText         *string  `json:"addressText"`
	TransactionProofURL *string  `json:"transactionProofUrl"`
	EstimatedWeight     *float64 `json:"estimatedWeight"`
	ActualWeight        *float64 `json:"actualWeight"`
	AgreedPrice         *float64 `json:"agreedPrice"`
	TotalPrice          *float64 `json:"totalPrice"`
	Status              string   `json:"status"`
	CreatedAt           Time     `json:"createdAt"`
	UpdatedAt           Time     `json:"updatedAt"`
}

type OrderItem struct {
	ID              string    `json:"id"`
	OrderID         string    `json:"orderId"`
	CategoryID      string    `json:"categoryId"`
	EstimatedWeight float64   `json:"estimatedWeight"`
	ActualWeight    *float64  `json:"actualWeight"`
	AgreedPrice     *float64  `json:"agreedPrice"`
	Notes           *string   `json:"notes"`
	Category        *Category `json:"category,omitempty"`
}

type Rating struct {
	ID         string     `json:"id"`
	OrderID    string     `json:"orderId"`
	RaterID    string     `json:"raterId"`
	RateeID    string     `json:"rateeId"`
	Score      int        `json:"score"`
	ReviewText *string    `json:"reviewText"`
	PhotoURL   *string    `json:"photoUrl"`
	CreatedAt  Time       `json:"createdAt"`
	Rater      *RaterInfo `json:"rater,omitempty"`
}

type RaterInfo struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	AvatarURL *string `json:"avatarUrl"`
}

// LatLng = koordinat dari kolom PostGIS User.location.
type LatLng struct {
	Lat float64
	Lng float64
}
