# 149. An ask for an answer is settled by the answer

Date: 2026-11-20

## Status

Accepted

## Context

`get_my_actions` kept an ask open after the operator had answered it in
the thread. The ask read "please give us an answer by tomorrow" (in
Russian, no question mark), and the reply was the full answer. The
resolver treated a reply to an ask without "?" as an acknowledgement
only, because for most asks the deliverable is work ("send the report")
and talk about it does not finish it. The reply also contained an
"in progress" phrase about the fix, which the acceptance rule matches.

## Decision

- `actions.AsksForAnswer` recognises asks whose deliverable is the reply
  itself: "give/send … answer/status/update/feedback", "let me know",
  "ответьте", "подскажите", "дайте знать", "какой статус".
- Such an ask is Answered by the operator's reply when the reply does not
  just accept it, or when it has at least ten words. A long answer often
  mentions work in progress; a bare "ok" or "checking, will get back"
  still leaves it open.
- Asks for work are unchanged: a reply without a done word stays an
  acknowledgement.

## Consequences

- Answered asks for an answer leave the due list and count under "done or
  answered".
- A ten-word acceptance ("ок, посмотрю вечером и …") to an ask for an
  answer now reads as an answer. Accepted: the operator wrote at length,
  and the item is still visible with due=all history.
