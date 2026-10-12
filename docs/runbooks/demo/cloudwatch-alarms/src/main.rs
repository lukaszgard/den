//! CloudWatch alarms that are firing in the environment's account: a runbook in
//! Rust. den sets AWS_PROFILE and AWS_REGION, which aws-config reads, so the
//! client needs no configuration. Read-only: cloudwatch:DescribeAlarms.
//!
//! The exit code is the result: 0 when no alarm is in ALARM, 1 when one is.

use aws_config::BehaviorVersion;
use aws_sdk_cloudwatch::error::DisplayErrorContext;
use aws_sdk_cloudwatch::primitives::DateTimeFormat;
use aws_sdk_cloudwatch::types::StateValue;
use aws_sdk_cloudwatch::Client;

#[tokio::main]
async fn main() {
    let config = aws_config::load_defaults(BehaviorVersion::latest()).await;
    let client = Client::new(&config);

    let mut firing = Vec::new();
    let mut pages = client
        .describe_alarms()
        .state_value(StateValue::Alarm)
        .into_paginator()
        .send();
    while let Some(page) = pages.next().await {
        match page {
            Ok(page) => firing.extend(page.metric_alarms.unwrap_or_default()),
            Err(err) => {
                eprintln!("cloudwatch-alarms: {}", DisplayErrorContext(&err));
                std::process::exit(1);
            }
        }
    }

    if firing.is_empty() {
        println!("no alarms firing");
        return;
    }
    println!("{} alarms firing:", firing.len());
    for alarm in &firing {
        let since = alarm
            .state_updated_timestamp()
            .and_then(|at| at.fmt(DateTimeFormat::DateTime).ok())
            .unwrap_or_default();
        println!("  {}  since {}", alarm.alarm_name().unwrap_or("?"), since);
    }
    std::process::exit(1);
}
