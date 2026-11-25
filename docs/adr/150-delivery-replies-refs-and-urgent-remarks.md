# 150. Delivery replies, posted references and urgent remarks

Date: 2026-11-25

## Status

Accepted

## Context

Three get_my_actions misreads in one week, and one empty search hit:

- A remark with an urgency word and no request ("… а сейчас горит))) Ну
  работаем) Все ок") was listed for days as "urgent, asked of you": the
  urgency word set a same-day deadline, and a dated message counts as an
  ask on its own.
- A request stayed open after it was delivered: the reply said
  "разобрались, затолкали, деплой в прод живой". The done lexicon had
  singular verbs only and no deploy verbs.
- "нужен taskid pls" stayed waiting after a colleague posted the key; a
  waiting item closed only on a done word.
- search_messages rendered a forwarded message as an empty body: its text
  lives in an attachment.

## Decision

- `Deadline.Dated` records whether a real date phrase was found;
  `OnlyUrgent()` is an urgency word with no date. Such a deadline no
  longer makes a message an ask by itself; a request word still does,
  and the deadline is kept on items that are asks.
- The done lexicon gains plural and deploy forms: сделали, отправили,
  скинули, прислали, закрыли, починили, исправили, выкатили, залили,
  задеплоил*, затолкал*, протолкнул*, "pushed to prod", "released".
  "на проде" is deliberately not a done phrase: "problem on prod" is not
  a delivery.
- A waiting item whose text asks for a reference (task id, ticket, link)
  is Answered by any reply carrying a tracker key or a URL.
- A search hit with an empty body renders its forwarded attachments
  ("↪ author: text"); a hit with its own text is unchanged.

## Consequences

- Fewer false "urgent" asks and stale waiting items.
- "срочно!!" alone in a DM is no longer an ask; a phrase rule cannot tell
  that from a remark, and the remark case was the one seen.
- Items that drop out of the search window are still not carried between
  runs; that needs state and stays a task.
