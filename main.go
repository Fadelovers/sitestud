package main

import (
	"database/sql"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"site/handlers"
	"site/login"
	"strconv"

	"time"

	"os"

	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
)

type Post struct {
	Id        int
	Title     string
	Anons     string
	Full_text string
	PhotoID   sql.NullInt64
	CreatedAt time.Time
}

type TemplateData struct {
	Posts           []Post
	IsAuthenticated bool
}

type Data struct {
	Post            Post
	IsAuthenticated bool
}

var Posts = []Post{}
var Show_Posts = Post{}

type Comment struct {
	Id        int
	PostID    int
	UserEmail string
	Content   string
	CreatedAt time.Time
}
type PageData struct {
	Post            Post
	Comments        []Comment
	IsAuthenticated bool
	UserEmail       string
}

func connectToDB() (*sql.DB, error) {
	log.Println("Connecting to DB with DSN:", os.Getenv("DATABASE_URL"))
	dsn := os.Getenv("DATABASE_PUBLIC_URL")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=123 dbname=postgres sslmode=disable"
	}
	return sql.Open("postgres", dsn)
}

// index — обработчик для страницы /main (список постов без авторизации, просто шаблон)
func index(w http.ResponseWriter, r *http.Request) {
	t, err := template.ParseFiles("html/conect.html", "html/header_for_connect.html")
	if err != nil {
		panic(err)
	}
	t.ExecuteTemplate(w, "connect", Posts)
}

// creat — обработчик страницы создания нового поста
func creat(w http.ResponseWriter, r *http.Request) {
	t, err := template.ParseFiles("html/creat.html", "html/header.html")
	if err != nil {
		http.Error(w, "error", http.StatusBadRequest)
		return
	}
	t.ExecuteTemplate(w, "creat", nil)
}

func main_func(w http.ResponseWriter, r *http.Request) {
	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Error connecting to the database", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	rows, err := db.Query("SELECT id, title, anons, full_text FROM post")
	if err != nil {
		http.Error(w, "Error querying the database", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var posts []Post
	for rows.Next() {
		var post Post
		if err := rows.Scan(&post.Id, &post.Title, &post.Anons, &post.Full_text); err != nil {
			http.Error(w, "Error scanning post", http.StatusInternalServerError)
			return
		}
		posts = append(posts, post)
	}

	// 2) Проверяем авторизацию через ту же сессию, что и в addCommentHandler
	session, _ := login.Store.Get(r, "session-name")
	auth, _ := session.Values["authenticated"].(bool)
	isAuth := auth

	data := TemplateData{
		Posts:           posts,
		IsAuthenticated: isAuth,
	}

	tmpl := template.Must(template.ParseFiles(
		"html/header.html",
		"html/title.html",
		"html/main.html",
	))

	if err := tmpl.ExecuteTemplate(w, "main", data); err != nil {
		http.Error(w, "Error executing template: "+err.Error(), http.StatusInternalServerError)
	}
}

func save_article(w http.ResponseWriter, r *http.Request) {
	// 1) Multipart-разбор
	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		http.Error(w, "Parse form error: "+err.Error(), http.StatusBadRequest)
		return
	}

	title := r.FormValue("title")
	anons := r.FormValue("anons")
	fullText := r.FormValue("full_text")

	// 2) Подключаемся к БД
	db, err := connectToDB()
	if err != nil {
		http.Error(w, "DB connection error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// 3) Читаем файл photo из формы
	var photoID sql.NullInt64
	file, handler, err := r.FormFile("photo")
	if err == nil {
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, "Read file error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		var newFileID int
		err = db.QueryRow(
			`INSERT INTO files (name, description, data)
             VALUES ($1, $2, $3)
             RETURNING id`,
			handler.Filename, "", data,
		).Scan(&newFileID)
		if err != nil {
			http.Error(w, "Insert file error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		photoID = sql.NullInt64{Int64: int64(newFileID), Valid: true}
	} else {
		photoID = sql.NullInt64{Valid: false}
	}

	// 4) Вставляем статью
	if photoID.Valid {
		_, err = db.Exec(
			`INSERT INTO post (title, anons, full_text, photo_id)
             VALUES ($1, $2, $3, $4)`,
			title, anons, fullText, photoID.Int64,
		)
	} else {
		_, err = db.Exec(
			`INSERT INTO post (title, anons, full_text, photo_id)
             VALUES ($1, $2, $3, NULL)`,
			title, anons, fullText,
		)
	}
	if err != nil {
		http.Error(w, "Insert post error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// show_post — обработчик GET /post/{id}, чтобы показать один пост подробнo
func show_post(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	idStr := vars["id"]
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Неверный ID", http.StatusBadRequest)
		return
	}

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка подключения к БД: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// 1) Читаем сам пост
	var p Post
	err = db.QueryRow(
		"SELECT id, title, anons, full_text, photo_id, created_at FROM post WHERE id = $1",
		id,
	).Scan(&p.Id, &p.Title, &p.Anons, &p.Full_text, &p.PhotoID, &p.CreatedAt)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Ошибка чтения из БД: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 2) Загружаем комментарии
	rows, err := db.Query(
		"SELECT id, post_id, user_email, content, created_at FROM comments WHERE post_id = $1 ORDER BY created_at ASC",
		id,
	)
	if err != nil {
		http.Error(w, "Ошибка чтения комментариев: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var comments []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.Id, &c.PostID, &c.UserEmail, &c.Content, &c.CreatedAt); err != nil {
			http.Error(w, "Ошибка сканирования комментария: "+err.Error(), http.StatusInternalServerError)
			return
		}
		comments = append(comments, c)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "Ошибка после обхода комментариев: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 3) Проверяем авторизацию через сессию Gorilla
	session, _ := login.Store.Get(r, "session-name")
	auth, _ := session.Values["authenticated"].(bool)
	userEmail, _ := session.Values["user_email"].(string)
	isAuth := auth && userEmail != ""

	// 4) Формируем данные и рендерим шаблон
	data := PageData{
		Post:            p,
		Comments:        comments,
		IsAuthenticated: isAuth,
		UserEmail:       userEmail,
	}
	tmpl := template.Must(template.ParseFiles("html/header.html", "html/Show.html"))
	if err := tmpl.ExecuteTemplate(w, "Show", data); err != nil {
		http.Error(w, "Ошибка рендеринга шаблона: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// Delete — обработчик POST /Delet/{id}, чтобы удалить пост
func Delete(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Error connecting to the database", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// Параметризованный DELETE
	_, err = db.Exec("DELETE FROM post WHERE id = $1", vars["id"])
	if err != nil {
		http.Error(w, "Error deleting from the database", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func editPostFormHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	idStr := vars["id"]
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Некорректный ID", http.StatusBadRequest)
		return
	}

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка подключения к БД: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// Извлекаем все поля, включая photo_id
	var p Post
	err = db.QueryRow(
		"SELECT id, title, anons, full_text, photo_id, created_at FROM post WHERE id = $1",
		id,
	).Scan(&p.Id, &p.Title, &p.Anons, &p.Full_text, &p.PhotoID, &p.CreatedAt)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, "Ошибка чтения из БД: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Проверяем, авторизован ли пользователь
	isAuth := login.IsAuthenticated(r)

	tmpl := template.Must(template.ParseFiles("html/header.html", "html/edit.html"))
	data := struct {
		Post            Post
		IsAuthenticated bool
	}{
		Post:            p,
		IsAuthenticated: isAuth,
	}

	if err := tmpl.ExecuteTemplate(w, "edit", data); err != nil {
		http.Error(w, "Ошибка рендеринга: "+err.Error(), http.StatusInternalServerError)
	}
}

func updatePostHandler(w http.ResponseWriter, r *http.Request) {
	// Разбираем форму с возможной загрузкой файла
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "Ошибка разбора формы: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Читаем поля
	idStr := r.FormValue("id")
	title := r.FormValue("title")
	anons := r.FormValue("anons")
	fullText := r.FormValue("full_text")
	deletePhoto := r.FormValue("delete_photo") // если установлено, будет "1"

	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Некорректный ID поста", http.StatusBadRequest)
		return
	}

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка подключения к БД: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// Сначала получим текущий photo_id, чтобы знать, откуда начинать
	var currentPhotoID sql.NullInt64
	err = db.QueryRow("SELECT photo_id FROM post WHERE id = $1", id).Scan(&currentPhotoID)
	if err != nil {
		http.Error(w, "Ошибка чтения текущего photo_id: "+err.Error(), http.StatusInternalServerError)
		return
	}

	newPhotoID := currentPhotoID

	if deletePhoto == "1" {
		newPhotoID = sql.NullInt64{Valid: false}
	}

	// Проверим, загружен ли новый файл
	file, handler, err := r.FormFile("photo")
	if err == nil {
		// Если новый файл есть, то всегда заменяем (независимо от delete_photo)
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, "Ошибка чтения файла: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// Сохраняем новый файл в таблицу files
		var insertedID int
		err = db.QueryRow(
			`INSERT INTO files (name, description, data)
             VALUES ($1, $2, $3) RETURNING id`,
			handler.Filename, "", data,
		).Scan(&insertedID)
		if err != nil {
			http.Error(w, "Ошибка вставки файла: "+err.Error(), http.StatusInternalServerError)
			return
		}
		newPhotoID = sql.NullInt64{Int64: int64(insertedID), Valid: true}

	}

	if newPhotoID.Valid {
		// Обновляем, указывая newPhotoID
		_, err = db.Exec(
			`UPDATE post
             SET title = $1, anons = $2, full_text = $3, photo_id = $4
             WHERE id = $5`,
			title, anons, fullText, newPhotoID.Int64, id,
		)
	} else {
		// Обновляем, сбрасывая photo_id на NULL
		_, err = db.Exec(
			`UPDATE post
             SET title = $1, anons = $2, full_text = $3, photo_id = NULL
             WHERE id = $4`,
			title, anons, fullText, id,
		)
	}
	if err != nil {
		http.Error(w, "Ошибка обновления поста: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// После успешного обновления перенаправляем на страницу просмотра поста
	http.Redirect(w, r, fmt.Sprintf("/post/%d", id), http.StatusSeeOther)
}

func ServeFileHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	idStr := vars["id"]
	log.Println("ServeFileHandler: got request for file ID =", idStr)

	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Invalid file ID", http.StatusBadRequest)
		return
	}

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "DB connection error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	var data []byte
	var name string
	err = db.QueryRow("SELECT data, name FROM files WHERE id = $1", id).Scan(&data, &name)
	if err == sql.ErrNoRows {
		log.Println("ServeFileHandler: no row for id =", id)
		http.NotFound(w, r)
		return
	} else if err != nil {
		log.Println("ServeFileHandler: DB query error:", err)
		http.Error(w, "DB query error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("ServeFileHandler: sending %d bytes for file ID=%d, name=%s\n", len(data), id, name)

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "inline; filename=\""+name+"\"")
	w.Write(data)
}

func showPostHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	idStr := vars["id"]
	postID, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Некорректный ID", http.StatusBadRequest)
		return
	}

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка подключения к БД: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// 1) Читаем статью
	var p Post
	err = db.QueryRow(
		"SELECT id, title, anons, full_text, photo_id, created_at FROM post WHERE id = $1",
		postID,
	).Scan(&p.Id, &p.Title, &p.Anons, &p.Full_text, &p.PhotoID, &p.CreatedAt)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, "Ошибка чтения из БД: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 2) Читаем комментарии к этой статье
	rows, err := db.Query(
		"SELECT id, post_id, user_email, content, created_at FROM comments WHERE post_id = $1 ORDER BY created_at ASC",
		postID,
	)
	if err != nil {
		http.Error(w, "Ошибка чтения комментариев: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var comments []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.Id, &c.PostID, &c.UserEmail, &c.Content, &c.CreatedAt); err != nil {
			http.Error(w, "Ошибка сканирования комментария: "+err.Error(), http.StatusInternalServerError)
			return
		}
		comments = append(comments, c)
	}
	if err = rows.Err(); err != nil {
		http.Error(w, "Ошибка при обходе комментариев: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 3) Проверяем, залогинен ли пользователь
	userEmail := ""
	if cookie, err := r.Cookie("user_email"); err == nil {
		userEmail = cookie.Value
	}
	isAuth := (userEmail != "")

	// 4) Формируем данные для шаблона
	data := PageData{
		Post:            p,
		Comments:        comments,
		IsAuthenticated: isAuth,
		UserEmail:       userEmail,
	}

	tmpl := template.Must(template.ParseFiles("html/header.html", "html/Show.html"))
	if err := tmpl.ExecuteTemplate(w, "Show", data); err != nil {
		http.Error(w, "Ошибка рендеринга шаблона: "+err.Error(), http.StatusInternalServerError)
	}
}

func addCommentHandler(w http.ResponseWriter, r *http.Request) {
	// Разрешаем только POST
	if r.Method != http.MethodPost {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	// 1) Проверяем сессию
	session, err := login.Store.Get(r, "session-name")
	if err != nil {
		http.Error(w, "Ошибка сессии: "+err.Error(), http.StatusInternalServerError)
		return
	}
	auth, _ := session.Values["authenticated"].(bool)
	userEmail, _ := session.Values["user_email"].(string)
	if !auth || userEmail == "" {
		http.Error(w, "Нужно войти, чтобы оставить комментарий", http.StatusUnauthorized)
		return
	}

	// 2) Парсим форму
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ошибка разбора формы: "+err.Error(), http.StatusBadRequest)
		return
	}
	postIDStr := r.FormValue("post_id")
	content := r.FormValue("content")
	if postIDStr == "" || content == "" {
		http.Error(w, "Все поля обязательны", http.StatusBadRequest)
		return
	}
	postID, err := strconv.Atoi(postIDStr)
	if err != nil {
		http.Error(w, "Некорректный ID статьи", http.StatusBadRequest)
		return
	}

	// 3) Сохраняем комментарий
	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка подключения к БД: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	_, err = db.Exec(
		"INSERT INTO comments (post_id, user_email, content) VALUES ($1, $2, $3)",
		postID, userEmail, content,
	)
	if err != nil {
		http.Error(w, "Ошибка добавления комментария: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 4) Редирект обратно на страницу поста
	http.Redirect(w, r, fmt.Sprintf("/post/%d", postID), http.StatusSeeOther)
}

func todaysNewsHandler(w http.ResponseWriter, r *http.Request) {
	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка подключения к БД: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	rows, err := db.Query(`
        SELECT sub.id,
               sub.title,
               sub.anons,
               sub.full_text,
               sub.photo_id,
               sub.created_at
          FROM (
            SELECT * FROM get_todays_posts()
          ) AS sub
         ORDER BY sub.created_at DESC
    `)
	if err != nil {
		http.Error(w, "Ошибка чтения сегодняшних новостей: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var posts []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(
			&p.Id,
			&p.Title,
			&p.Anons,
			&p.Full_text,
			&p.PhotoID,
			&p.CreatedAt,
		); err != nil {
			http.Error(w, "Ошибка сканирования поста: "+err.Error(), http.StatusInternalServerError)
			return
		}
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "Ошибка после итерации: "+err.Error(), http.StatusInternalServerError)
		return
	}

	data := struct {
		Posts           []Post
		IsAuthenticated bool
		Today           string
	}{
		Posts:           posts,
		IsAuthenticated: login.IsAuthenticated(r),
		Today:           time.Now().Format("02.01.2006"),
	}

	tmpl, err := template.ParseFiles("html/Today.html", "html/header.html", "html/title.html")
	if err != nil {
		http.Error(w, "Error parsing templates", http.StatusInternalServerError)
		return
	}

	if err := tmpl.ExecuteTemplate(w, "Today", data); err != nil {
		http.Error(w, "Ошибка рендеринга шаблона: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// handlerRequest — настройка маршрутов и запуск HTTP‑сервера
func handlerRequest() {
	rtr := mux.NewRouter()

	rtr.Handle("/css/", http.StripPrefix("/css/", http.FileServer(http.Dir("./css/"))))
	rtr.HandleFunc("/post/edit/{id:[0-9]+}", editPostFormHandler).Methods("GET")
	rtr.HandleFunc("/post/update", updatePostHandler).Methods("POST")
	rtr.HandleFunc("/main", index).Methods("GET")
	rtr.HandleFunc("/creat", creat).Methods("GET")
	rtr.HandleFunc("/", main_func).Methods("GET")
	rtr.HandleFunc("/save_article", save_article).Methods("POST")
	rtr.HandleFunc("/UserCheck", login.UserCheck).Methods("POST")
	rtr.HandleFunc("/post/{id:[0-9]+}", show_post).Methods("GET")
	rtr.HandleFunc("/logout", login.LogoutHandler).Methods("POST")
	rtr.HandleFunc("/Delet/{id:[0-9]+}", Delete).Methods("POST")
	rtr.HandleFunc("/confirm", handlers.ConfirmUser).Methods("POST")
	http.HandleFunc("/ConfirmUser", handlers.ConfirmUser)
	rtr.HandleFunc("/SaveUser", handlers.SaveUser).Methods("POST")
	rtr.HandleFunc("/reg", handlers.Register).Methods("GET", "POST")
	rtr.HandleFunc("/file/{id:[0-9]+}", ServeFileHandler).Methods("GET")
	rtr.HandleFunc("/confirm", handlers.ConfirmPage).Methods("GET")
	rtr.HandleFunc("/ConfirmUser", handlers.ConfirmCodeHandler).Methods("POST")
	rtr.HandleFunc("/comment/add", addCommentHandler).Methods("POST")
	rtr.HandleFunc("/today", todaysNewsHandler).Methods("GET")
	http.ListenAndServe(":8080", rtr)

}

func main() {
	handlerRequest()
}
