/* Reminder Discord Bot by IMcPwn.
 * Copyright 2016 Carleton Stuberg

 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at

 * http://www.apache.org/licenses/LICENSE-2.0

 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.

 * For the latest code and contact information visit: http://imcpwn.com
 */

package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	log "github.com/Sirupsen/logrus"
	"github.com/bwmarrin/discordgo"
	_ "github.com/mattn/go-sqlite3"
)

// Deprecated. Use SafeDB instead.
// db is the SQLite database used by Reminder
//var db *sql.DB

// bot is the Discord user the program is running as
var bot *discordgo.User

// DATEFORMAT is the command line flag for the format of dates
var DATEFORMAT *string

// SafeDatabase stores a SQL database and a mutex for safe access
type SafeDatabase struct {
	db  *sql.DB
	mux sync.Mutex
}

// SafeDB provides safe access to the database
var SafeDB SafeDatabase

// ratelimit limits how many messages can be sent in an amount of time
var ratelimit chan func()

func main() {
	TOKEN := flag.String("t", "", "Discord authentication token.")
	DBPATH := flag.String("db", "reminder.db", "Database file name.")
	DATEFORMAT = flag.String("date", "2006-01-02 15:04:05", "Date format.")
	LOGDEST := flag.String("log", "reminder.log", "Log file name.")
	LOGLEVEL := flag.String("loglevel", "info", "Log level. Options: info, debug, warn, error")
	SLEEPTIME := flag.Int("sleep", 10, "Seconds to sleep in between checking the database.")
	LIMIT := flag.Int("limit", 100, "The rate limit for sending messages (excluding reminders).")
	flag.Parse()

	if *TOKEN == "" {
		flag.Usage()
		fmt.Println("-t option is required")
		return
	}
	
	if _, err := os.Stat(*LOGDEST); os.IsNotExist(err) {
		_, err := os.Create(*LOGDEST)
		if err != nil {
			fmt.Println("Can't create log file")
			fmt.Println(err)
			return
		}
	}
	logFile, err := os.OpenFile(*LOGDEST, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		fmt.Println("Can't open log file")
		fmt.Println(err)
		return
	}
	defer logFile.Close()

	log.SetOutput(logFile)
	// TODO: Make this configurable
	log.SetFormatter(&log.JSONFormatter{})

	if *LOGLEVEL == "info" {
		log.SetLevel(log.InfoLevel)
	} else if *LOGLEVEL == "debug" {
		log.SetLevel(log.DebugLevel)
	} else if *LOGLEVEL == "warn" {
		log.SetLevel(log.WarnLevel)
	} else {
		log.SetLevel(log.ErrorLevel)
	}

	log.Debug("Log set up")

	dg, err := discordgo.New(*TOKEN)
	if err != nil {
		flag.Usage()
		log.WithFields(log.Fields{
			"error": err,
		}).Error("Unable to create discord session")
		fmt.Println(err)
		return
	}
	log.Info("Authenticated to Discord")

	dg.AddHandler(messageCreate)

	err = dg.Open()
	if err != nil {
		flag.Usage()
		log.WithFields(log.Fields{
			"error": err,
		}).Error("Unable to open websocket")
		fmt.Println(err)
		return
	}
	log.Info("Websocket opened")

	bot, err = dg.User("@me")
	if err != nil {
		flag.Usage()
		log.WithFields(log.Fields{
			"error": err,
		}).Error("Unable to log in")
		fmt.Println(err)
		return
	}
	log.Info("Logged in successfully as " + bot.Username)

	err = safeCreateDB(*DBPATH)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err,
		}).Error("Unable to create database")
		fmt.Println(err)
		return
	}
	log.Info("Database created/connected")
	defer SafeDB.db.Close()

	dg.UpdateStatus(0, "!RemindMe help")
	log.Info("Set status to \"Playing\" !RemindMe help")

	// Create ratelimit
	ratelimit = make(chan func(), *LIMIT)
	go handle(ratelimit)

	// Call database search as goroutine
	go searchDatabase(dg, *SLEEPTIME)

	fmt.Println("Welcome to Reminder by IMcPwn.\nCopyright (C) 2016 Carleton Stuberg\nPress enter to quit.")
	fmt.Println("If the program quits unexpectedly, check the log for details.")
	log.Debug("End of main()")
	var input string
	fmt.Scanln(&input)
	return
}

// Create database if it doesn't already exist.
func safeCreateDB(path string) error {
	var err error
	SafeDB.db, err = sql.Open("sqlite3", path)
	if err != nil {
		return err
	}

	// Statement to create reminder table if it doesn't already exist
	statement := `
	create table if not exists reminder
	(
		ID integer primary key,
		currTime datetime,
		remindTime datetime,
		message text,
		userid text,
		reminded integer
	)
	`

	_, err = SafeDB.db.Exec(statement)
	if err != nil {
		return err
	}
	return nil
}

// Delay by 1 second each time the rate limit is exceeded.
func handle(ratelimit chan func()) {
	for f := range ratelimit {
		f()
		time.Sleep(1 * time.Second)
	}
}

// Search database for date equal to current time or later.
// If found, send the reminder to the user.
// Afterwords update reminded status in database.
func searchDatabase(s *discordgo.Session, sleep int) {
	searchDatabaseLogger := log.WithFields(log.Fields{
		"function": "searchDatabase",
	})
	searchDatabaseLogger.Debug("searchDatabase called")
	for {
		// Lock the mutex while accessing the database
		SafeDB.mux.Lock()
		rows, err := SafeDB.db.Query("select ID, currTime, remindTime, message, userid, reminded from reminder")
		if err != nil {
			searchDatabaseLogger.WithFields(log.Fields{
				"error": err,
			}).Warn("Executing query for all fields")
			SafeDB.mux.Unlock()
			time.Sleep(time.Duration(sleep) * time.Second)
			return
		}
		// Stores the IDs of the reminders that have been sent
		var idsDone []int
		for rows.Next() {
			var id int
			var currTime time.Time
			var remindTime time.Time
			var message string
			var userid string
			var reminded bool
			err = rows.Scan(&id, &currTime, &remindTime, &message, &userid, &reminded)
			if err != nil {
				searchDatabaseLogger.WithFields(log.Fields{
					"error": err,
				}).Warn("Getting data from row")
				SafeDB.mux.Unlock()
				time.Sleep(time.Duration(sleep) * time.Second)
				return
			}
			if time.Now().UTC().After(remindTime) && !reminded {
				ch, err := s.UserChannelCreate(userid)
				if err != nil {
					searchDatabaseLogger.WithFields(log.Fields{
						"reminderID": id,
						"error":      err,
					}).Warn("Creating private message to user")
					SafeDB.mux.Unlock()
					time.Sleep(time.Duration(sleep) * time.Second)
					return
				}
				fullmessage := fmt.Sprintf("*Responding to request at %s UTC.*\n"+
					"You wanted me to remind you: **%s**", currTime.Format(*DATEFORMAT), message)
				_, err = s.ChannelMessageSend(ch.ID, fullmessage)
				if err != nil {
					searchDatabaseLogger.WithFields(log.Fields{
						"reminderID": id,
						"error":      err,
					}).Warn("Sending private message to user")
					SafeDB.mux.Unlock()
					time.Sleep(time.Duration(sleep) * time.Second)
					return
				}
				searchDatabaseLogger.WithFields(log.Fields{
					"reminderID": id,
				}).Info("Reminded user")
				// Add reminder ID to ID list to set reminded to true later
				idsDone = append(idsDone, id)
			}
		}
		rows.Close()
		// Update reminded messages "reminded" status to true
		for i := 0; i < len(idsDone); i++ {
			statement := fmt.Sprintf("update reminder\n"+
				"set reminded = 1\n"+
				"where id = '%d'", idsDone[i])
			_, err = SafeDB.db.Exec(statement)
			if err != nil {
				searchDatabaseLogger.WithFields(log.Fields{
					"reminderID": idsDone[i],
					"error":      err,
				}).Warn("Updating reminder status to \"reminded\"")
				SafeDB.mux.Unlock()
				time.Sleep(time.Duration(sleep) * time.Second)
				return
			}
			searchDatabaseLogger.WithFields(log.Fields{
				"reminderID": idsDone[i],
			}).Info("Updated reminded flag to true")
		}
		// Unlock the mutex when finished
		SafeDB.mux.Unlock()
		searchDatabaseLogger.WithFields(log.Fields{
			"sleepTime": sleep,
		}).Debug("Sleeping")
		time.Sleep(time.Duration(sleep) * time.Second)
	}
}

// Send message to author with @theirname.
// Only usable from MessageCreate event.
func sendMention(s *discordgo.Session, m *discordgo.MessageCreate, content string) {
	// Rate limit sending mention to user
	ratelimit <- func() {
		_, err := s.ChannelMessageSend(m.ChannelID, "@"+m.Author.Username+m.Author.Discriminator+" "+content)
		if err != nil {
			log.WithFields(log.Fields{
				"function": "sendMention",
				"UserID":   m.Author.ID,
				"Username": m.Author.Username,
				"error":    err,
			}).Warn("Unable to send message to user")
			return
		}
	}
}

// Send bot usage information to user.
func printUsage(s *discordgo.Session, m *discordgo.MessageCreate) {
	desc := "```Hi, I'm a bot. What you entered was not a valid command. See below for usage.\n" +
		"Find my source code at imcpwn.com\n\n" +
		"I will remind you with your message after the time has elapsed.\nExclude the brackets when typing the command.\n\n" +
		"Usage: !RemindMe [number] [minute(s)/hour(s)/day(s)] [reminder message]\n" +
		"Example: !RemindMe 5 minutes This message will be sent to you in 5 minutes!" +
		"```"
	sendMention(s, m, desc)
}

// Attempt to add reminder information to database
// and send confirmation message to user if it was successfully scheduled.
func botMentioned(s *discordgo.Session, m *discordgo.MessageCreate) {
	botMentionedLogger := log.WithFields(log.Fields{
		"function": "botMentioned",
	})
	botMentionedLogger.Debug("botMentioned called")
	content := strings.Split(m.Content, " ")
	if len(content) < 4 || len(content) > 30 || utf8.RuneCountInString(m.Content) > 100 {
		// TODO: Should this fail silently?
		printUsage(s, m)
	} else {
		remindNumIn := content[1]
		timeTypeIn := content[2]
		// TODO: Improve method of string concatenation
		var message string
		for i := 3; i < len(content); i++ {
			message = message + " " + content[i]
		}
		remindDate := time.Now().UTC()
		var timeType time.Duration

		switch timeTypeIn {
		case "minutes":
			timeType = time.Minute
		case "minute":
			timeType = time.Minute
		case "hours":
			timeType = time.Hour
		case "hour":
			timeType = time.Hour
		case "day":
			timeType = (time.Hour * 24)
		case "days":
			timeType = (time.Hour * 24)
		default:
			// TODO: Should this fail silently?
			printUsage(s, m)
			botMentionedLogger.WithFields(log.Fields{
				"UserID":      m.Author.ID,
				"Username":    m.Author.Username,
				"remindNumIn": remindNumIn,
				"error":       "case not found",
			}).Warn("Setting time type")
			return
		}

		remindNum, err := strconv.Atoi(remindNumIn)
		if err != nil {
			// TODO: Should this fail silently?
			printUsage(s, m)
			botMentionedLogger.WithFields(log.Fields{
				"UserID":      m.Author.ID,
				"Username":    m.Author.Username,
				"remindNumIn": remindNumIn,
				"error":       err,
			}).Warn("Converting remindNumIn to a number")
			return
		}

		remindDate = remindDate.Add(time.Duration(remindNum) * timeType)

		statement := `
		select max(id) from reminder
		`

		// Lock the mutex while using the database
		SafeDB.mux.Lock()

		rows, err := SafeDB.db.Query(statement)
		if err != nil {
			botMentionedLogger.WithFields(log.Fields{
				"statement": statement,
				"error":     err,
			}).Warn("Querying database for largest ID")
			SafeDB.mux.Unlock()
			return
		}

		var id int

		for rows.Next() {
			err = rows.Scan(&id)
			if err != nil {
				botMentionedLogger.WithFields(log.Fields{
					"RemindID":  id,
					"statement": statement,
					"error":     err,
				}).Warn("Querying database for largest id. Could just be the first time running the program.")
				id = 1
			}
		}
		rows.Close()

		// Begin prepared statement
		tx, err := SafeDB.db.Begin()
		if err != nil {
			botMentionedLogger.WithFields(log.Fields{
				"RemindID": id,
				"error":    err,
			}).Warn("Beginning SQL prepared statement")
			SafeDB.mux.Unlock()
			return
		}
		// Prepared SQL statement. Screw injections.
		stmt, err := tx.Prepare("insert into reminder(ID, currTime, remindTime, message, userid, reminded) values(?, ?, ?, ?, ?, ?)")
		if err != nil {
			botMentionedLogger.WithFields(log.Fields{
				"RemindID": id,
				"error":    err,
			}).Warn("Preparing SQL Statement")
			SafeDB.mux.Unlock()
			return
		}

		_, err = stmt.Exec(id+1, time.Now().UTC().Format(*DATEFORMAT), remindDate.Format(*DATEFORMAT), message, m.Author.ID, 0)
		if err != nil {
			botMentionedLogger.WithFields(log.Fields{
				"RemindID": id,
				"error":    err,
			}).Warn("Inserting reminder into database")
			SafeDB.mux.Unlock()
			return
		}
		tx.Commit()
		stmt.Close()

		botMentionedLogger.WithFields(log.Fields{
			"RemindID": id,
		}).Info("Reminder added to database")

		// Unlock the mutex when finished
		SafeDB.mux.Unlock()

		// First try to private message the user the status, otherwise try to @mention the user there is an error
		ch, err := s.UserChannelCreate(m.Author.ID)
		if err != nil {
			botMentionedLogger.WithFields(log.Fields{
				"RemindID": id,
				"UserID":   m.Author.ID,
				"Username": m.Author.Username,
				"error":    err,
			}).Warn("Creating private message to user")
			return
		}
		// Rate limit sending confirmation message
		ratelimit <- func() {
			_, err = s.ChannelMessageSend(ch.ID, "Got it. I'll remind you here at "+remindDate.Format(*DATEFORMAT)+" UTC.")
			if err != nil {
				botMentionedLogger.WithFields(log.Fields{
					"RemindID": id,
					"UserID":   m.Author.ID,
					"Username": m.Author.Username,
					"error":    err,
				}).Warn("Sending private message to user")
				return
			}
			botMentionedLogger.WithFields(log.Fields{
				"RemindID": id,
			}).Info("Reminder confirmation sent to user")
		}
	}
}

// This function will be called every time a new message is created
// on any channel that the autenticated user has access to.
// This function is triggered by @botname mentions and !RemindMe mentions.
func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	messageCreateLogger := log.WithFields(log.Fields{
		"function": "messageCreate",
	})
	messageCreateLogger.Debug("messageCreate called")

	if m.Author.ID == bot.ID {
		messageCreateLogger.Debug("Not responding to self")
		return
	}
	if len(m.Mentions) > 0 {
		if m.Mentions[0].ID == bot.ID {
			messageCreateLogger.WithFields(log.Fields{
				"UserID":   m.Author.ID,
				"Username": m.Author.Username,
			}).Debug("@Mentioned")
			botMentioned(s, m)
		}
	} else if strings.HasPrefix(m.Content, "!RemindMe") {
		messageCreateLogger.WithFields(log.Fields{
			"UserID":   m.Author.ID,
			"Username": m.Author.Username,
		}).Debug("!RemindMe mentioned")
		botMentioned(s, m)
	} else {
		messageCreateLogger.Debug("No mentions and message doesn't start with !RemindMe")
	}
}
