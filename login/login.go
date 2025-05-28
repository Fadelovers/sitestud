package login

import (
	"database/sql"
	"net/http"

	"os"

	"github.com/gorilla/sessions"
	_ "github.com/lib/pq"
)

type User struct {
	Email, Password string
}

// connectToDB подключается к той же базе PostgreSQL
func connectToDB() (*sql.DB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=123 dbname=postgres sslmode=disable"
	}
	return sql.Open("postgres", dsn)
}

var Store = sessions.NewCookieStore([]byte("something-very-secret"))

// UserCheck — обработчик POST /UserCheck: проверяем email/password по таблице regist
func UserCheck(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	password := r.FormValue("password")
	if email == "" || password == "" {
		http.Error(w, "Error empty login or password", http.StatusBadRequest)
		return
	}

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Error connecting to the database", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// Получаем все записи из regist
	res, err := db.Query("SELECT email, password FROM users")
	if err != nil {
		http.Error(w, "Error querying the database", http.StatusInternalServerError)
		return
	}
	defer res.Close()

	var user User
	var IsValidUser bool
	for res.Next() {
		err := res.Scan(&user.Email, &user.Password)
		if err != nil {
			http.Error(w, "Error reading database", http.StatusInternalServerError)
			return
		}
		if user.Email == email && user.Password == password {
			IsValidUser = true
			break
		}
	}

	if IsValidUser {
		session, _ := Store.Get(r, "session-name")
		session.Values["authenticated"] = true
		session.Values["user_email"] = email
		session.Save(r, w)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	} else {
		http.Error(w, "Error no", http.StatusUnauthorized)
	}
}

// IsAuthenticated проверяет, залогинен ли пользователь, по куки‑сессии
func IsAuthenticated(r *http.Request) bool {
	session, _ := Store.Get(r, "session-name")
	auth, ok := session.Values["authenticated"].(bool)
	return ok && auth
}

// LogoutHandler обнуляет сессию и редиректит на /
func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	session, err := Store.Get(r, "session-name")
	if err != nil {
		http.Error(w, "Error getting session", http.StatusInternalServerError)
		return
	}
	session.Options.MaxAge = -1
	if err := session.Save(r, w); err != nil {
		http.Error(w, "Error saving session", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
