import 'package:flutter/material.dart';

import 'screens/login_screen.dart';
import 'screens/note_detail_screen.dart';
import 'screens/notes_list_screen.dart';
import 'screens/search_screen.dart';
import 'services/api_service.dart';

/// The MentisEterna Flutter application.
///
/// To change the server URL, pass it to [MentisEternaApp] or update the
/// default value below.
class MentisEternaApp extends StatelessWidget {
  final String serverBaseUrl;

  const MentisEternaApp({
    super.key,
    this.serverBaseUrl = 'http://localhost:8080',
  });

  @override
  Widget build(BuildContext context) {
    final api = ApiService(baseUrl: serverBaseUrl);

    return MaterialApp(
      title: 'MentisEterna',
      debugShowCheckedModeBanner: false,
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(
          seedColor: const Color(0xFF6750A4),
          brightness: Brightness.light,
        ),
        useMaterial3: true,
        inputDecorationTheme: InputDecorationTheme(
          border: OutlineInputBorder(
            borderRadius: BorderRadius.circular(8),
          ),
          contentPadding:
              const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
        ),
      ),
      darkTheme: ThemeData(
        colorScheme: ColorScheme.fromSeed(
          seedColor: const Color(0xFF6750A4),
          brightness: Brightness.dark,
        ),
        useMaterial3: true,
        inputDecorationTheme: InputDecorationTheme(
          border: OutlineInputBorder(
            borderRadius: BorderRadius.circular(8),
          ),
          contentPadding:
              const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
        ),
      ),
      themeMode: ThemeMode.system,
      initialRoute: '/login',
      onGenerateRoute: (settings) {
        switch (settings.name) {
          case '/login':
            return MaterialPageRoute(
              builder: (_) => LoginScreen(api: api),
            );

          case '/notes':
            return MaterialPageRoute(
              builder: (_) => NotesListScreen(api: api),
            );

          case '/notes/detail':
            final noteId = settings.arguments as int;
            return MaterialPageRoute(
              builder: (_) => NoteDetailScreen(
                api: api,
                noteId: noteId,
              ),
            );

          case '/search':
            return MaterialPageRoute(
              builder: (_) => SearchScreen(api: api),
            );

          default:
            return MaterialPageRoute(
              builder: (_) => LoginScreen(api: api),
            );
        }
      },
    );
  }
}
