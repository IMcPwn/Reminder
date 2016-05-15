Reminder [![Go Report Card](https://goreportcard.com/badge/github.com/imcpwn/Reminder)](https://goreportcard.com/report/github.com/imcpwn/Reminder) [![Build Status](https://travis-ci.org/IMcPwn/Reminder.svg?branch=master)](https://travis-ci.org/IMcPwn/Reminder)
===================
Discord bot to remind users after the requested time has elapsed.

Installing
===================
Reminder is officially supported on Go 1.6.1+.

```sh
go get github.com/IMcPwn/Reminder
```
The executable should be located under GOPATH/bin/Reminder(.exe)

This assumes you have declared $GOPATH or %GOPATH% on Windows.
For help setting up a Go environment, see: https://golang.org/doc/install

Usage
===================
!RemindMe [number] [minute(s)/hour(s)/day(s)] [reminder message]

Command line options:

-date string

	Date format.
-db string

	Database file name.
-limit int

	The rate limit for sending messages (excluding reminders).
-log string

	Log file name.
-loglevel string

	Log level. Options: info, debug, warn, error
-sleep int

	Seconds to sleep in between checking the database.
-t string

	Discord authentication token.

License
===================
[The Apache License, Version 2.0](http://www.apache.org/licenses/LICENSE-2.0)


Contact
===================
This plugin is made by IMcPwn .

Contact information such as email, twitter, and other methods of contact are avaliable here: http://imcpwn.com
