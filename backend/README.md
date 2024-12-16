# Inhalt der Datei: /backend/backend/README.md

# Projektname

Dies ist ein Go-Anwendungsprojekt, das eine einfache Backend-Architektur implementiert.

## Verzeichnisstruktur

- **cmd/**: Enthält die Hauptanwendung.
  - **main.go**: Einstiegspunkt der Anwendung.
  
- **pkg/**: Enthält wiederverwendbare Pakete.
  - **server/**: Serverlogik und -methoden.
    - **server.go**: Definition der Serverstruktur und ihrer Methoden.
  - **config/**: Konfigurationsmanagement.
    - **config.go**: Struktur und Funktion zum Laden der Konfiguration.

- **internal/**: Enthält interne Pakete, die nicht exportiert werden.
  - **handlers/**: HTTP-Anfrageverarbeitung.
    - **handlers.go**: Definition der Handler für verschiedene Endpunkte.
  - **models/**: Datenmodelle der Anwendung.
    - **models.go**: Definition der Datenstrukturen für die Datenbank.
  - **routes/**: Routenmanagement.
    - **routes.go**: Konfiguration der Routen und Zuweisung der Handler.

## Installation

1. Klonen Sie das Repository:
   ```
   git clone <repository-url>
   ```
2. Wechseln Sie in das Verzeichnis:
   ```
   cd backend
   ```
3. Abhängigkeiten installieren:
   ```
   go mod tidy
   ```

## Ausführung

Um die Anwendung zu starten, führen Sie den folgenden Befehl aus:
```
go run cmd/main.go
```

## Lizenz

Dieses Projekt ist unter der MIT-Lizenz lizenziert.