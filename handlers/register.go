package handlers

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/smtp"
	"sync"

	"os"

	_ "github.com/lib/pq"
)

type User struct {
	Email            string
	Password         string
	PasswordConfirm  string
	ConfirmationCode string
	Confirmed        bool
}

var users = make(map[string]User)
var mu sync.Mutex

func connectToDB() (*sql.DB, error) {
	dsn := os.Getenv("DATABASE_PUBLIC_URL")
	if dsn == "" {
		dsn = "host=nozomi.proxy.rlwy.net port=10901 user=postgres password=AqbxOKFcClXSBPUvcvSUZBiOVorFdUfW dbname=railway sslmode=disable"
	}
	return sql.Open("postgres", dsn)
}

func generateConfirmationCode() string {
	bytes := make([]byte, 6)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func sendEmail(to, code string) {
	from := "nkayet@yandex.ru"
	password := "tvmnkyymrebzlhoy" // Пароль приложения
	smtpHost := "smtp.yandex.ru"
	smtpPort := "587"

	auth := smtp.PlainAuth("", from, password, smtpHost)
	msg := []byte("To: " + to + "\r\n" +
		"Subject: Confirm your account\r\n" +
		"\r\n" +
		"Your confirmation code is: " + code + "\r\n")

	err := smtp.SendMail(smtpHost+":"+smtpPort, auth, from, []string{to}, msg)
	if err != nil {
		log.Println("Error sending email:", err)
	} else {
		log.Println("Email sent to:", to)
	}
}

func SaveUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	email := r.FormValue("email")
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")

	if email == "" || password == "" || passwordConfirm == "" {
		http.Error(w, "Не все данные", http.StatusBadRequest)
		return
	}

	if password != passwordConfirm {
		http.Error(w, "Passwords do not match", http.StatusBadRequest)
		return
	}

	confirmationCode := generateConfirmationCode()

	mu.Lock()
	users[email] = User{
		Email:            email,
		Password:         password,
		PasswordConfirm:  passwordConfirm,
		ConfirmationCode: confirmationCode,
		Confirmed:        false,
	}
	mu.Unlock()

	sendEmail(email, confirmationCode)

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Database connection error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	_, err = db.Exec(
		`INSERT INTO regist (email, password, confirm_password, confirmation_code, confirmed)
		 VALUES ($1, $2, $3, $4, $5)`,
		email, password, passwordConfirm, confirmationCode, false,
	)
	if err != nil {
		http.Error(w, "Database insert error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Println("User registered with email:", email)
	http.Redirect(w, r, "/confirm?email="+email, http.StatusSeeOther)
}

func Register(w http.ResponseWriter, r *http.Request) {
	t, err := template.ParseFiles("html/reg.html", "html/header_for_connect.html")
	if err != nil {
		fmt.Fprintf(w, err.Error())
		return
	}
	t.ExecuteTemplate(w, "reg", nil)
}

func ConfirmPage(w http.ResponseWriter, r *http.Request) {
	email := r.URL.Query().Get("email")
	if email == "" {
		http.Error(w, "Email is required", http.StatusBadRequest)
		return
	}

	t, err := template.ParseFiles("html/confirm.html", "html/header_for_connect.html")
	if err != nil {
		fmt.Fprintf(w, err.Error())
		return
	}
	t.ExecuteTemplate(w, "confirm", map[string]string{"Email": email})
}

func ConfirmUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	code := r.FormValue("code")
	email := r.FormValue("email")

	mu.Lock()
	user, exists := users[email]
	mu.Unlock()
	if !exists {
		http.Error(w, "User not found", http.StatusBadRequest)
		return
	}

	if user.ConfirmationCode != code {
		http.Error(w, "Invalid confirmation code", http.StatusBadRequest)
		return
	}

	user.Confirmed = true
	mu.Lock()
	users[email] = user
	mu.Unlock()

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Database connection error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// Обновляем поле confirmed в PostgreSQL; используем $1 и $2
	_, err = db.Exec(
		`UPDATE regist SET confirmed = $1
		 WHERE email = $2`,
		true, email,
	)
	if err != nil {
		http.Error(w, "Database update error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/confirmation-success", http.StatusSeeOther)
}

func ConfirmationSuccess(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "html/confirmation-success.html")
}

func ConfirmEmailPage(w http.ResponseWriter, r *http.Request) {
	t, err := template.ParseFiles("html/confirm_code.html", "html/header.html")
	if err != nil {
		http.Error(w, "Ошибка шаблона: "+err.Error(), http.StatusInternalServerError)
		return
	}
	t.ExecuteTemplate(w, "confirm", nil)
}

func ConfirmCodeHandler(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	code := r.FormValue("code")

	if email == "" || code == "" {
		http.Error(w, "Заполните все поля", http.StatusBadRequest)
		return
	}

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка подключения к БД: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	var dbCode string
	var password string

	err = db.QueryRow("SELECT confirmation_code, password FROM regist WHERE email = $1", email).Scan(&dbCode, &password)
	if err == sql.ErrNoRows {
		http.Error(w, "Пользователь не найден", http.StatusBadRequest)
		return
	} else if err != nil {
		http.Error(w, "Ошибка чтения БД: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if dbCode != code {
		http.Error(w, "Неверный код", http.StatusUnauthorized)
		return
	}

	// Код верный — вставляем в users
	_, err = db.Exec(
		"INSERT INTO users (email, password) VALUES ($1, $2)",
		email, password,
	)
	if err != nil {
		http.Error(w, "Ошибка добавления в users: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Редиректим на вход
	http.Redirect(w, r, "/main", http.StatusSeeOther)
}
