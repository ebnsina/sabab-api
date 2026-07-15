# Changelog

All notable changes to Sabab are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html) once it cuts a
first tagged release.

The dashboard renders a public, human-friendly version of this file at
`/changelog`. Keep every entry user-facing — describe what changed for the person
using Sabab, never internal milestones, roadmap codenames, or architecture.

## [Unreleased]

### Added

- **Logs.** Ingest, store, search and tail your application logs. Every log
  carries its trace, so from an error you can jump straight to the logs emitted
  around it. Log search uses the same query language as everywhere else
  (`severity:>=warn service:checkout timeout`), and a live tail streams new lines
  as they arrive.
- **Log capture in the SDKs.** A structured logger (`Sabab.log.info(…)`) plus
  opt-in capture of your existing `console` output, both linked to the active
  trace.
- **Alerting.** Get notified on new issues, regressions, and error-frequency
  spikes — delivered to Slack, Discord, webhooks, or email, with throttling so
  one noisy bug does not page you repeatedly.
- **Light theme.** The dashboard now follows your system setting by default, with
  a manual light / dark / system switch in Settings.
- **Settings.** Profile and appearance moved into a dedicated Settings page.

### Changed

- A refreshed dashboard: cleaner navigation, a consistent header, and a more
  refined, modern look throughout.

## [0.1.0] — Error Monitoring

### Added

- **Error monitoring, end to end.** Browser and server errors are captured,
  grouped into issues, de-minified back to your original source, and shown with a
  full stack-trace viewer.
- **Smart grouping** that survives releases: the same bug across two deploys —
  with shifted line numbers and a new build — stays one issue instead of
  flooding you with duplicates.
- **Sensitive-data scrubbing** before anything is stored, so passwords, tokens
  and card numbers never land in your event data.
- **The Sabab SDKs** for browser and Node — designed never to slow down or break
  your app, and honest about anything they drop.
- **Self-hostable** with a single command.
