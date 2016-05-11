/* Reminder Discord Bot by IMcPwn.
 * Copyright 2016 IMcPwn 

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
    "fmt"
    "flag"
    "time"
    "os"
    "database/sql"
    "strings"
    "strconv"
    
    "github.com/bwmarrin/discordgo"
    log "github.com/Sirupsen/logrus"
    _ "github.com/mattn/go-sqlite3"
)

// SQLite database
var db *sql.DB
// Format of dates
var DATEFORMAT *string

func main() {
    TOKEN := flag.String("t", "", "Discord authentication token")
    DBPATH := flag.String("db", "reminder.db", "Database path")
    DATEFORMAT = flag.String("date", "2006-01-02 15:04:05", "Date format")
    LOGDEST := flag.String("log", "reminder.log", "Log name")
    LOGLEVEL := flag.String("loglevel", "info", "Log level")
    SLEEPTIME := flag.Int("sleep", 10, "Seconds to sleep in between checking the database")
    flag.Parse()
    
    if _, err := os.Stat(*LOGDEST); os.IsNotExist(err) {
        _, err := os.Create(*LOGDEST)
        if err != nil {
            fmt.Println("Can't create log")
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

    if *TOKEN == "" {
        flag.Usage()
        fmt.Println("-t option is required")
        return
    }

    dg, err := discordgo.New(*TOKEN)
    if err != nil {
        flag.Usage()
        log.WithFields(log.Fields{
            "error":    err,
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
            "error":    err,
        }).Error("Unable to open websocket")
        fmt.Println(err)
        return
    }
    log.Info("Websocket opened")

    prefix, err := dg.User("@me")
    if err != nil {
        flag.Usage()
        log.WithFields(log.Fields{
            "error":    err,
        }).Error("Unable to log in")
        fmt.Println(err)
        return
    }
    log.Info("Logged in successfully as " + prefix.Username)

    db, err = safeCreateDB(*DBPATH);
    if err != nil {
       log.WithFields(log.Fields{
            "error":    err,
       }).Error("Unable to create database")
       fmt.Println(err)
       return
    }
    log.Info("Database created/connected")
    defer db.Close()

    // Call database search as goroutine
    go searchDatabase(dg, db, *SLEEPTIME);

    fmt.Println("Welcome to Reminder by IMcPwn.\nCopyright (C) 2016 IMcPwn \nPress enter to quit.")
    fmt.Println("If the program quits unexpectedly, check the log for details.")
    log.Info("End of main()")
    var input string
    fmt.Scanln(&input)
    return
}

// Create database if it doesn't already exist.
// TODO: Clean this up.
func safeCreateDB(path string) (*sql.DB, error) {
   if _, err := os.Stat(path); os.IsNotExist(err) {
        db, err := sql.Open("sqlite3", path)
	    if err != nil {
            return nil, err
	    }
        // Create reminder table
        statement := `
        create table reminder
        (
            ID integer primary key,
            currTime datetime,
            remindTime datetime,
            message text,
            userid text,
            reminded integer
        )
        `
        _, err = db.Exec(statement)
        if err != nil {
            return nil, err
        }
    }
    db, err := sql.Open("sqlite3", path)
    if err != nil {
        return nil, err
    }
    return db, nil
}

// Search database for date equal to current time or later.
func searchDatabase(s *discordgo.Session, db *sql.DB, sleep int) {
    searchDatabaseLogger := log.WithFields(log.Fields{
        "function": "searchDatabase",
    })
    for {
        rows, err := db.Query("select ID, currTime, remindTime, message, userid, reminded from reminder")
        if err != nil {
            searchDatabaseLogger.WithFields(log.Fields{
                "error":    err,
            }).Error("Executing query")
            os.Exit(1)
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
                    "error":    err,
                }).Error("Getting data from row")
                os.Exit(1)
            }
            if time.Now().UTC().After(remindTime) && !reminded {
                ch, err := s.UserChannelCreate(userid)
                if err != nil {
                    searchDatabaseLogger.WithFields(log.Fields{
                        "reminderID": id,
                        "error":    err,
                    }).Error("Creating private message to user")
                    os.Exit(1)
                }
                fullmessage := fmt.Sprintf("*Responding to request at %s UTC.*\n" +
                "You wanted me to remind you: **%s**", currTime.Format(*DATEFORMAT), message) 
                _, err = s.ChannelMessageSend(ch.ID, fullmessage)
                if err != nil {
                    searchDatabaseLogger.WithFields(log.Fields{
                        "reminderID": id,
                        "error":    err,
                    }).Error("Sending private message to user")
                    os.Exit(1)
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
            statement := fmt.Sprintf("update reminder\n" +
            "set reminded = 1\n" +
            "where id = '%d'", idsDone[i])
            _, err = db.Exec(statement)
            if err != nil {
                searchDatabaseLogger.WithFields(log.Fields{
                    "reminderID": idsDone[i],
                    "error":    err,
                }).Error("Updating reminder status to \"reminded\"")
                os.Exit(1)
            }
            searchDatabaseLogger.WithFields(log.Fields{
                "reminderID": idsDone[i],
            }).Info("Updated reminded flag to true")
        }
        idsDone = nil
        searchDatabaseLogger.WithFields(log.Fields{
            "sleep": sleep,
        }).Debug("Sleeping")
        time.Sleep(time.Duration(sleep) * time.Second)
    }
}


// Send @mention message to author from MessageCreate event.
func sendMention(s *discordgo.Session, m *discordgo.MessageCreate, content string) {
    _, err := s.ChannelMessageSend(m.ChannelID, "@" + m.Author.Username + m.Author.Discriminator + " " + content)
    if err != nil {
        log.WithFields(log.Fields{
            "function": "sendMention",
            "UserID": m.Author.ID,
            "Username": m.Author.Username,
            "error":    err,
        }).Error("Unable to send message to user")
    }
}


// Print bot usage.
func printUsage(s *discordgo.Session, m *discordgo.MessageCreate) {
    prefix, err := s.User("@me")
    if err != nil {
        log.WithFields(log.Fields{
            "function": "printUsage",
            "error":    err,
        }).Error("Unable to log in")
        os.Exit(1)
    }
    desc := fmt.Sprintf("```Hi, I'm a bot.\n" +
    "Find my source code at imcpwn.com\n\n" +
    "I will remind you with your message after the time has elapsed.\nExclude the brackets when typing the command.\n\n" + 
    "Usage: @%s [number] [minute(s)/hour(s)/day(s)] [reminder message]\n" +
    "Example: @%s 5 minutes Send me a reminder in 5 minutes!" + 
    "```", prefix.Username + prefix.Discriminator, prefix.Username + prefix.Discriminator)
    sendMention(s, m, desc)
}

// Attempt to add reminder information to database
// and notify user about it.
func botMentioned(s *discordgo.Session, m*discordgo.MessageCreate) {
    botMentionedLogger := log.WithFields(log.Fields{
        "function": "botMentioned",
    })
    content := strings.Split(m.Content, " ")
    if len(content) < 4 || len(content) > 50 || content[1] == "0" {
        printUsage(s, m)
        return
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
            case "minutes": timeType = time.Minute
            case "minute": timeType = time.Minute
            case "hours": timeType = time.Hour
            case "hour": timeType = time.Hour
            case "day": timeType = (time.Hour * 24)
            case "days": timeType = (time.Hour * 24)
            default: 
                printUsage(s, m)
                botMentionedLogger.WithFields(log.Fields{
                    "UserID": m.Author.ID,
                    "Username": m.Author.Username,
                    "remindNumIn": remindNumIn,
                    "error":    "case not found",
                }).Warn("Setting time type")
                return
        }

        remindNum, err := strconv.Atoi(remindNumIn)
        if err != nil {
            printUsage(s, m)
            botMentionedLogger.WithFields(log.Fields{
                "UserID": m.Author.ID,
                "Username": m.Author.Username,
                "remindNumIn": remindNumIn,
                "error":    err,
            }).Warn("Converting remindNumIn to a number")
            return
        }

        remindDate = remindDate.Add(time.Duration(remindNum) * timeType)

        statement := `
        select max(id) from reminder
        `

        rows, err := db.Query(statement)
        if err != nil {
            botMentionedLogger.WithFields(log.Fields{
                "statement": statement,
                "error":    err,
            }).Error("Querying database for largest ID")
            os.Exit(1)
        }

        var id int

        for rows.Next() {
            err = rows.Scan(&id)
            if err != nil {
                botMentionedLogger.WithFields(log.Fields{
                    "RemindID": id,
                    "statement": statement,
                    "error":    err,
                }).Warn("Querying database for largest id. Could just be the first time running the program.")
                id = 1
            }
        }
        rows.Close()

        // Begin prepared statement
        tx, err := db.Begin()
        if err != nil {
            sendMention(s, m, " Error scheduling reminder.")
            botMentionedLogger.WithFields(log.Fields{
                "RemindID": id,
                "error":    err,
            }).Warn("Beginning SQL prepared statement")
            return
        }
        // Prepared SQL statement. Screw injections.
        stmt, err := tx.Prepare("insert into reminder(ID, currTime, remindTime, message, userid, reminded) values(?, ?, ?, ?, ?, ?)")
        if err != nil {
            sendMention(s, m, " Error scheduling reminder.")
            botMentionedLogger.WithFields(log.Fields{
                "RemindID": id,
                "error":    err,
            }).Warn("Preparing SQL Statement")
            return
        }

        _, err = stmt.Exec(id + 1, time.Now().UTC().Format(*DATEFORMAT), remindDate.Format(*DATEFORMAT), message, m.Author.ID, 0)
        if err != nil {
            sendMention(s, m, "Error scheduling reminder.")
            botMentionedLogger.WithFields(log.Fields{
                "RemindID": id,
                "error":    err,
            }).Warn("Inserting reminder into database")
            return
        }
        tx.Commit()
        stmt.Close()
        
        botMentionedLogger.WithFields(log.Fields{
            "RemindID": id,
        }).Info("Reminder added to database")

        // First try to private message the user the status, otherwise try to @mention the user there is an error
        ch, err := s.UserChannelCreate(m.Author.ID)
        if err != nil {
            sendMention(s, m, "I'm having trouble private messaging you.")
            botMentionedLogger.WithFields(log.Fields{
                "RemindID": id,
                "UserID": m.Author.ID,
                "Username": m.Author.Username,
                "error":    err,
            }).Warn("Creating private message to user")
            return
        }
        _, err = s.ChannelMessageSend(ch.ID, "Got it. I'll remind you here at " + remindDate.Format(*DATEFORMAT) + " UTC.")
        if err != nil {
            sendMention(s, m, "I'm having trouble private messaging you.")
            botMentionedLogger.WithFields(log.Fields{
                "RemindID": id,
                "UserID": m.Author.ID,
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

// This function will be called every time a new message is created 
// on any channel that the autenticated user has access to.
// This function is responsible for responding to @mentions.
func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
    messageCreateLogger := log.WithFields(log.Fields{
        "function": "messageCreate",
    })
    if len(m.Mentions) < 1 {
        messageCreateLogger.Debug("Not Mentioned")
        return
    }
    prefix, err := s.User("@me")
    if err != nil {
        messageCreateLogger.WithFields(log.Fields{
            "error":    err,
        }).Error("Unable to log in")
        os.Exit(1)
    }
    if m.Mentions[0].ID == prefix.ID  {
        messageCreateLogger.Info("Mentioned")
        botMentioned(s, m);
    }
}
