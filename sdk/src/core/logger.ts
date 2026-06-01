export type LogLevel = "debug" | "info" | "warn" | "error";

export interface LogEvent {
  level: LogLevel;
  message: string;
  fields?: Record<string, unknown>;
}

export type LogHandler = (event: LogEvent) => void;

export interface Logger {
  debug(message: string, fields?: Record<string, unknown>): void;
  info(message: string, fields?: Record<string, unknown>): void;
  warn(message: string, fields?: Record<string, unknown>): void;
  error(message: string, fields?: Record<string, unknown>): void;
}

function createLevelMethod(level: LogLevel, handler?: LogHandler): Logger[LogLevel] {
  return (message, fields) => {
    if (!handler) {
      return;
    }

    try {
      handler({ level, message, fields });
    } catch {
      // Logging must never break worker or client control flow.
    }
  };
}

export function createLogger(handler?: LogHandler): Logger {
  return {
    debug: createLevelMethod("debug", handler),
    info: createLevelMethod("info", handler),
    warn: createLevelMethod("warn", handler),
    error: createLevelMethod("error", handler),
  };
}
