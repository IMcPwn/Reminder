package main

import (
    "fmt"
    "flag"

    "github.com/bwmarrin/discordgo"
)

func main() {
    USERNAME := flag.String("u", "", "Username")
    PASSWORD := flag.String("p", "", "Password")
    flag.Parse()
    
    dg, err := discordgo.New(*USERNAME, *PASSWORD)
    if err != nil {
        flag.Usage()
        fmt.Println(err)
        return
    }
    
    // Open the websocket and begin listening.
    err = dg.Open()
    if err != nil {
        flag.Usage()
        fmt.Println(err)
        return
    }

    // Make sure we're logged in successfully
    prefix, err := dg.User("@me")
    if err != nil {
        flag.Usage()
        fmt.Println(err)
        return
    }
    fmt.Println("Logged in as " + prefix.ID)}