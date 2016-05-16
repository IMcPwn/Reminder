/*
  Reminder Discord Bot by IMcPwn.
  For the latest code and contact information visit: http://imcpwn.com

  Copyright (c) 2016, Carleton Stuberg
  All rights reserved.

  Redistribution and use in source and binary forms, with or without
  modification, are permitted provided that the following conditions are met:

  * Redistributions of source code must retain the above copyright notice, this
    list of conditions and the following disclaimer.

  * Redistributions in binary form must reproduce the above copyright notice,
    this list of conditions and the following disclaimer in the documentation
    and/or other materials provided with the distribution.

  * Neither the name of Reminder nor the names of its
    contributors may be used to endorse or promote products derived from
    this software without specific prior written permission.

  THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
  AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
  IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
  DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
  FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
  DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
  SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
  CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
  OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
  OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

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

func main() {
	TOKEN := flag.String("t", "", "Discord authentication token.")
	DBPATH := flag.String("db", "reminder.db", "Database file name.")
	DATEFORMAT = flag.String("date", "2006-01-02 15:04:05", "Date format.")
	LOGDEST := flag.String("log", "reminder.log", "Log file name.")
	LOGLEVEL := flag.String("loglevel", "info", "Log level. Options: info, debug, warn, error")
	SLEEPTIME := flag.Int("sleep", 10, "Seconds to sleep in between checking the database.")
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
				// TODO: Handle this better
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
	_, err := s.ChannelMessageSend(m.ChannelID, "@"+m.Author.Username+m.Author.Discriminator+" "+content)
	if err != nil {
		log.WithFields(log.Fields{
			"function": "sendMention",
			"UserID":   m.Author.ID,
			"Username": m.Author.Username,
			"Channel":  m.ChannelID,
			"error":    err,
		}).Warn("Sending message to user")
		return
	}
}

// Try to private message the user. If that fails @mention them where they
// sent the bot the original message.
func sendPrivateMessage(s *discordgo.Session, m *discordgo.MessageCreate, message string) {
	ch, err := s.UserChannelCreate(m.Author.ID)
	if err != nil {
		log.WithFields(log.Fields{
			"function": "sendPrivateMessage",
			"UserID":   m.Author.ID,
			"Username": m.Author.Username,
			"Channel":  ch.ID,
			"error":    err,
		}).Warn("Creating private message to user")
		// TODO: Delete this message after x seconds
		//sendMention(s, m, "I'm having trouble private messaging you.\n"+message)
		return
	}
	_, err = s.ChannelMessageSend(ch.ID, message)
	if err != nil {
		log.WithFields(log.Fields{
			"function": "sendPrivateMessage",
			"UserID":   m.Author.ID,
			"Username": m.Author.Username,
			"Channel":  ch.ID,
			"error":    err,
		}).Warn("Sending private message to user")
		// TODO: Delete this message after x seconds
		//sendMention(s, m, "I'm having trouble private messaging you.\n"+message)
		return
	}
}

// Send bot usage information to user.
func printUsage(s *discordgo.Session, m *discordgo.MessageCreate) {
	desc := "```Hi, I'm a bot. What you entered was not a valid command. See below for usage.\n" +
		"Find my source code at imcpwn.com\n\n" +
		"I will remind you with your message after the time has elapsed.\nExclude the brackets when typing the command.\n\n" +
		"Usage: !RemindMe [number] [minute(s)/hour(s)/day(s)] [reminder message]\n" +
		"Example: !RemindMe 5 minutes This message will be sent to you in 5 minutes!" +
		"Other commands: !RemindMe cancel --> Cancels all scheduled reminders" +
		"```"
	sendMention(s, m, desc)
}

// Respond to the "cancel" command for cancelling all scheduled reminders.
func cancelCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	cancelCommandLogger := log.WithFields(log.Fields{
		"function": "cancelCommand",
	})
	cancelCommandLogger.Debug("cancelCommand called")

	statement := fmt.Sprintf("update reminder\n"+
		"set reminded = 1\n"+
		"where userid = '%s'", m.Author.ID)

	// Lock the mutex while using the database
	SafeDB.mux.Lock()
	defer SafeDB.mux.Unlock()

	_, err := SafeDB.db.Exec(statement)
	if err != nil {
		cancelCommandLogger.WithFields(log.Fields{
			"UserID":   m.Author.ID,
			"Username": m.Author.Username,
			"error":    err,
		}).Warn("Cancelling reminders")
		// TODO: Delete this message after x seconds
		//sendPrivateMessage(s, m, "Error cancelling reminders")
		return
	}
	cancelCommandLogger.WithFields(log.Fields{
		"UserID":   m.Author.ID,
		"Username": m.Author.Username,
	}).Info("Cancelled reminders")

	sendPrivateMessage(s, m, "All scheduled reminders for you have been cancelled.")
}

// Respond to the remind command for adding a reminder to the database.
func remindCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	remindCommandLogger := log.WithFields(log.Fields{
		"function": "remindCommand",
	})
	remindCommandLogger.Debug("remindCommand called")

	content := strings.Split(m.Content, " ")
	remindNumIn := content[1]
	timeTypeIn := strings.ToUpper(content[2])
	// TODO: Improve method of string concatenation
	var message string
	for i := 3; i < len(content); i++ {
		message = message + " " + content[i]
	}
	remindDate := time.Now().UTC()
	var timeType time.Duration

	switch timeTypeIn {
	case "MINUTES":
		timeType = time.Minute
	case "MINUTE":
		timeType = time.Minute
	case "HOURS":
		timeType = time.Hour
	case "HOUR":
		timeType = time.Hour
	case "DAY":
		timeType = (time.Hour * 24)
	case "DAYS":
		timeType = (time.Hour * 24)
	default:
		// TODO: Delete this message after x seconds
		//printUsage(s, m)
		remindCommandLogger.WithFields(log.Fields{
			"UserID":      m.Author.ID,
			"Username":    m.Author.Username,
			"remindNumIn": remindNumIn,
			"error":       "case not found",
		}).Warn("Setting time type")
		return
	}

	remindNum, err := strconv.Atoi(remindNumIn)
	if err != nil {
		// TODO: Delete this message after x seconds
		//printUsage(s, m)
		remindCommandLogger.WithFields(log.Fields{
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
	defer SafeDB.mux.Unlock()

	rows, err := SafeDB.db.Query(statement)
	if err != nil {
		remindCommandLogger.WithFields(log.Fields{
			"statement": statement,
			"error":     err,
		}).Warn("Querying database for largest ID")
		// TODO: Delete this message after x seconds
		//sendPrivateMessage(s, m, "Error scheduling reminder.")
		return
	}

	var id int
	for rows.Next() {
		err = rows.Scan(&id)
		if err != nil {
			remindCommandLogger.WithFields(log.Fields{
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
		remindCommandLogger.WithFields(log.Fields{
			"RemindID": id,
			"error":    err,
		}).Warn("Beginning SQL prepared statement")
		// TODO: Delete this message after x seconds
		//sendPrivateMessage(s, m, "Error scheduling reminder.")
		return
	}
	// Prepared SQL statement. Screw injections.
	stmt, err := tx.Prepare("insert into reminder(ID, currTime, remindTime, message, userid, reminded) values(?, ?, ?, ?, ?, ?)")
	if err != nil {
		remindCommandLogger.WithFields(log.Fields{
			"RemindID": id,
			"error":    err,
		}).Warn("Preparing SQL Statement")
		// TODO: Delete this message after x seconds
		//sendPrivateMessage(s, m, "Error scheduling reminder.")
		return
	}

	_, err = stmt.Exec(id+1, time.Now().UTC().Format(*DATEFORMAT), remindDate.Format(*DATEFORMAT), message, m.Author.ID, 0)
	if err != nil {
		remindCommandLogger.WithFields(log.Fields{
			"RemindID": id,
			"error":    err,
		}).Warn("Inserting reminder into database")
		// TODO: Delete this message after x seconds
		//sendPrivateMessage(s, m, "Error scheduling reminder.")
		return
	}
	tx.Commit()
	stmt.Close()

	remindCommandLogger.WithFields(log.Fields{
		"RemindID": id,
	}).Info("Reminder added to database")

	sendPrivateMessage(s, m, "Got it. I'll remind you here at "+remindDate.Format(*DATEFORMAT)+" UTC.")
}

func botMentioned(s *discordgo.Session, m *discordgo.MessageCreate) {
	botMentionedLogger := log.WithFields(log.Fields{
		"function": "botMentioned",
	})
	botMentionedLogger.Debug("botMentioned called")
	content := strings.Split(m.Content, " ")
	// Check for entirely incorrect command
	if len(content) < 2 || len(content) > 30 || utf8.RuneCountInString(m.Content) > 100 {
		// TODO: Delete this message after x seconds
		printUsage(s, m)
		return
	}
	// Check for valid "cancel" command
	if strings.ToUpper(content[1]) == "CANCEL" {
		cancelCommand(s, m)
		return
	}
	// Check for valid "remind" command
	if len(content) > 3 {
		remindCommand(s, m)
		return
	}
}

// This function will be called every time a new message is created
// on any channel that the autenticated user has access to.
// This function will call botMentioned() if the message starts with !RemindMe or @botname.
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
	} else if strings.HasPrefix(strings.ToUpper(m.Content), "!REMINDME") {
		messageCreateLogger.WithFields(log.Fields{
			"UserID":   m.Author.ID,
			"Username": m.Author.Username,
		}).Debug("!RemindMe mentioned")
		botMentioned(s, m)
	} else {
		messageCreateLogger.Debug("No mentions and message doesn't start with !RemindMe")
	}
}
