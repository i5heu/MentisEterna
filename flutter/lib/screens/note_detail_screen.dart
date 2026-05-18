import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../models/models.dart';
import '../services/api_service.dart';

/// Full note detail view with tags, attachments, and plugin data.
class NoteDetailScreen extends StatefulWidget {
  final ApiService api;
  final int noteId;

  const NoteDetailScreen({
    super.key,
    required this.api,
    required this.noteId,
  });

  @override
  State<NoteDetailScreen> createState() => _NoteDetailScreenState();
}

class _NoteDetailScreenState extends State<NoteDetailScreen> {
  NoteDetail? _note;
  bool _loading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _loadNote();
  }

  Future<void> _loadNote() async {
    setState(() {
      _loading = true;
      _error = null;
    });

    try {
      final note = await widget.api.getNote(widget.noteId);
      if (mounted) {
        setState(() {
          _note = note;
          _loading = false;
        });
      }
    } on ApiException catch (e) {
      if (mounted) {
        setState(() {
          _error = e.message;
          _loading = false;
        });
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _error = 'Failed to load note: $e';
          _loading = false;
        });
      }
    }
  }

  Future<void> _togglePin() async {
    if (_note == null) return;
    try {
      final updated = await widget.api.setNotePin(
        _note!.id,
        !_note!.pinned,
      );
      if (mounted) setState(() => _note = updated);
    } on ApiException catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(e.message)),
        );
      }
    }
  }

  Future<void> _deleteNote() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Delete Note'),
        content: Text('Delete "${_note?.title ?? 'Untitled'}"?'),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(ctx).colorScheme.error,
            ),
            child: const Text('Delete'),
          ),
        ],
      ),
    );

    if (confirmed != true) return;

    try {
      await widget.api.deleteNote(widget.noteId);
      if (mounted) Navigator.of(context).pop();
    } on ApiException catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(e.message)),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(_note?.title.isNotEmpty == true
            ? _note!.title
            : 'Loading...'),
        actions: [
          if (_note != null) ...[
            IconButton(
              icon: Icon(
                _note!.pinned ? Icons.push_pin : Icons.push_pin_outlined,
                color: _note!.pinned
                    ? Theme.of(context).colorScheme.primary
                    : null,
              ),
              tooltip: _note!.pinned ? 'Unpin' : 'Pin',
              onPressed: _togglePin,
            ),
            IconButton(
              icon: const Icon(Icons.delete_outline),
              tooltip: 'Delete',
              onPressed: _deleteNote,
            ),
          ],
        ],
      ),
      body: _buildBody(),
    );
  }

  Widget _buildBody() {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }

    if (_error != null) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(_error!),
            const SizedBox(height: 16),
            FilledButton.tonal(
              onPressed: _loadNote,
              child: const Text('Retry'),
            ),
          ],
        ),
      );
    }

    if (_note == null) {
      return const Center(child: Text('Note not found.'));
    }

    return SingleChildScrollView(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Metadata row
          _MetadataRow(note: _note!),
          const SizedBox(height: 16),

          // Tags
          if (_note!.tags.isNotEmpty) ...[
            Wrap(
              spacing: 6,
              runSpacing: 4,
              children: _note!.tags
                  .map((tag) => Chip(
                        label: Text(tag),
                        materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                        visualDensity: VisualDensity.compact,
                      ))
                  .toList(),
            ),
            const SizedBox(height: 16),
          ],

          // Body (markdown as plain text for now)
          if (_note!.body.isNotEmpty) ...[
            Text(
              'Content',
              style: Theme.of(context).textTheme.titleSmall?.copyWith(
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                    fontWeight: FontWeight.w600,
                  ),
            ),
            const SizedBox(height: 8),
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: Theme.of(context).colorScheme.surfaceContainerHighest,
                borderRadius: BorderRadius.circular(8),
              ),
              child: SelectableText(
                _note!.body,
                style: const TextStyle(height: 1.5),
              ),
            ),
          ],

          // Attachments
          if (_note!.attachments.isNotEmpty) ...[
            const SizedBox(height: 24),
            Text(
              'Attachments',
              style: Theme.of(context).textTheme.titleSmall?.copyWith(
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                    fontWeight: FontWeight.w600,
                  ),
            ),
            const SizedBox(height: 8),
            ...(_note!.attachments.map((file) => ListTile(
                  leading: Icon(
                    file.isImage ? Icons.image : Icons.insert_drive_file,
                  ),
                  title: Text(file.filename),
                  subtitle:
                      Text('${_formatBytes(file.sizeBytes)} — ${file.mimeType}'),
                  dense: true,
                ))),
          ],

          // Plugin details
          if (_note!.plugin != null) ...[
            const SizedBox(height: 24),
            Text(
              'Plugin: ${_note!.plugin!.type}',
              style: Theme.of(context).textTheme.titleSmall?.copyWith(
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                    fontWeight: FontWeight.w600,
                  ),
            ),
            if (_note!.plugin!.config != null) ...[
              const SizedBox(height: 8),
              _JsonCard(data: _note!.plugin!.config!, label: 'Config'),
            ],
            if (_note!.plugin!.view != null) ...[
              const SizedBox(height: 8),
              _JsonCard(data: _note!.plugin!.view!, label: 'View'),
            ],
          ],
        ],
      ),
    );
  }

  String _formatBytes(int bytes) {
    if (bytes < 1024) return '$bytes B';
    if (bytes < 1024 * 1024) return '${(bytes / 1024).toStringAsFixed(1)} KB';
    return '${(bytes / (1024 * 1024)).toStringAsFixed(1)} MB';
  }
}

/// Metadata row showing creation date, update date, and note type.
class _MetadataRow extends StatelessWidget {
  final NoteDetail note;

  const _MetadataRow({required this.note});

  @override
  Widget build(BuildContext context) {
    final dateFormat = DateFormat.yMMMd().add_jm();
    final createdAt = DateTime.tryParse(note.createdAt);
    final updatedAt = DateTime.tryParse(note.updatedAt);

    return Row(
      children: [
        Icon(Icons.calendar_today,
            size: 14, color: Theme.of(context).colorScheme.onSurfaceVariant),
        const SizedBox(width: 4),
        Text(
          createdAt != null ? dateFormat.format(createdAt) : note.createdAt,
          style: TextStyle(
            fontSize: 13,
            color: Theme.of(context).colorScheme.onSurfaceVariant,
          ),
        ),
        const SizedBox(width: 16),
        Icon(Icons.update,
            size: 14, color: Theme.of(context).colorScheme.onSurfaceVariant),
        const SizedBox(width: 4),
        Text(
          updatedAt != null ? dateFormat.format(updatedAt) : note.updatedAt,
          style: TextStyle(
            fontSize: 13,
            color: Theme.of(context).colorScheme.onSurfaceVariant,
          ),
        ),
        const Spacer(),
        if (note.type != 'standard')
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
            decoration: BoxDecoration(
              color: Theme.of(context).colorScheme.secondaryContainer,
              borderRadius: BorderRadius.circular(4),
            ),
            child: Text(
              note.type,
              style: TextStyle(
                fontSize: 12,
                color: Theme.of(context).colorScheme.onSecondaryContainer,
              ),
            ),
          ),
      ],
    );
  }
}

/// Simple card showing a JSON object.
class _JsonCard extends StatelessWidget {
  final Map<String, dynamic> data;
  final String label;

  const _JsonCard({required this.data, required this.label});

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            label,
            style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 13),
          ),
          const SizedBox(height: 4),
          SelectableText(
            _formatMap(data, 0),
            style: const TextStyle(fontSize: 12, height: 1.4),
          ),
        ],
      ),
    );
  }

  String _formatMap(Map<String, dynamic> map, int indent) {
    final buf = StringBuffer();
    final prefix = '  ' * indent;
    for (final entry in map.entries) {
      if (entry.value is Map) {
        buf.writeln('$prefix${entry.key}:');
        buf.write(_formatMap(
            Map<String, dynamic>.from(entry.value as Map), indent + 1));
      } else if (entry.value is List) {
        buf.writeln('$prefix${entry.key}: [');
        for (final item in entry.value as List) {
          buf.writeln('$prefix  - $item');
        }
        buf.writeln('$prefix]');
      } else {
        buf.writeln('$prefix${entry.key}: ${entry.value}');
      }
    }
    return buf.toString();
  }
}
