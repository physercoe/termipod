import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../models/action_bar_config.dart';
import '../../providers/action_bar_provider.dart';
import '../../theme/design_colors.dart';

/// Settings screen for customizing action bar groups and buttons.
///
/// Allows reordering groups via drag handles, editing group contents,
/// adding/removing groups, and resetting to profile defaults.
class ActionBarSettingsScreen extends ConsumerStatefulWidget {
  const ActionBarSettingsScreen({super.key});

  @override
  ConsumerState<ActionBarSettingsScreen> createState() =>
      _ActionBarSettingsScreenState();
}

class _ActionBarSettingsScreenState
    extends ConsumerState<ActionBarSettingsScreen> {
  @override
  Widget build(BuildContext context) {
    final state = ref.watch(actionBarProvider);
    final profile = state.activeProfile;
    final groups = profile.groups;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;

    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Customize Toolbar',
          style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
        ),
        actions: [
          PopupMenuButton<String>(
            onSelected: (value) {
              if (value == 'reset') {
                _confirmReset(context);
              }
            },
            itemBuilder: (context) => [
              const PopupMenuItem(
                value: 'reset',
                child: Text('Reset to Default'),
              ),
            ],
          ),
        ],
      ),
      body: Column(
        children: [
          // Profile name header
          Padding(
            padding: const EdgeInsets.all(16),
            child: Row(
              children: [
                Icon(Icons.view_column,
                    size: 20,
                    color: isDark
                        ? DesignColors.textSecondary
                        : DesignColors.textSecondaryLight),
                const SizedBox(width: 8),
                Text(
                  'Profile: ${profile.name}',
                  style: TextStyle(
                    fontSize: 14,
                    color: isDark
                        ? DesignColors.textSecondary
                        : DesignColors.textSecondaryLight,
                  ),
                ),
              ],
            ),
          ),
          // Reorderable group list
          Expanded(
            child: ReorderableListView.builder(
              padding: const EdgeInsets.symmetric(horizontal: 8),
              itemCount: groups.length,
              onReorder: (oldIndex, newIndex) {
                ref
                    .read(actionBarProvider.notifier)
                    .reorderGroups(oldIndex, newIndex);
              },
              itemBuilder: (context, index) {
                final group = groups[index];
                return _GroupTile(
                  key: ValueKey(group.id),
                  group: group,
                  isDark: isDark,
                  onEdit: () => _editGroup(context, group),
                  onDelete: groups.length > 1
                      ? () => _confirmDeleteGroup(context, group)
                      : null,
                );
              },
            ),
          ),
          // Add group button
          SafeArea(
            top: false,
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: SizedBox(
                width: double.infinity,
                child: OutlinedButton.icon(
                  onPressed: () => _addGroup(context),
                  icon: const Icon(Icons.add),
                  label: const Text('Add Group'),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: colorScheme.primary,
                    padding: const EdgeInsets.symmetric(vertical: 12),
                  ),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }

  void _confirmReset(BuildContext context) {
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Reset to Default'),
        content: const Text(
          'This will reset all groups to the default configuration for this profile.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () {
              final state = ref.read(actionBarProvider);
              ref
                  .read(actionBarProvider.notifier)
                  .resetProfileToDefault(state.activeProfileId);
              Navigator.pop(ctx);
            },
            child: const Text('Reset'),
          ),
        ],
      ),
    );
  }

  void _confirmDeleteGroup(BuildContext context, ActionBarGroup group) {
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Delete Group'),
        content: Text('Delete "${group.name}" and all its buttons?'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: Colors.red),
            onPressed: () {
              ref.read(actionBarProvider.notifier).deleteGroup(group.id);
              Navigator.pop(ctx);
            },
            child: const Text('Delete'),
          ),
        ],
      ),
    );
  }

  void _addGroup(BuildContext context) {
    final nameController = TextEditingController();
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('New Group'),
        content: TextField(
          controller: nameController,
          autofocus: true,
          decoration: const InputDecoration(
            labelText: 'Group Name',
            border: OutlineInputBorder(),
            isDense: true,
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () {
              final name = nameController.text.trim();
              if (name.isNotEmpty) {
                final newGroup = ActionBarGroup(
                  id: 'custom_${DateTime.now().millisecondsSinceEpoch}',
                  name: name,
                  buttons: const [],
                );
                ref.read(actionBarProvider.notifier).addGroup(newGroup);
                Navigator.pop(ctx);
              }
            },
            child: const Text('Create'),
          ),
        ],
      ),
    );
  }

  void _editGroup(BuildContext context, ActionBarGroup group) {
    Navigator.push(
      context,
      MaterialPageRoute(
        builder: (context) => _GroupEditorScreen(group: group),
      ),
    );
  }
}

class _GroupTile extends StatelessWidget {
  final ActionBarGroup group;
  final bool isDark;
  final VoidCallback onEdit;
  final VoidCallback? onDelete;

  const _GroupTile({
    super.key,
    required this.group,
    required this.isDark,
    required this.onEdit,
    this.onDelete,
  });

  @override
  Widget build(BuildContext context) {
    return Card(
      margin: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      child: ListTile(
        leading: const Icon(Icons.drag_handle),
        title: Text(
          group.name,
          style: const TextStyle(fontWeight: FontWeight.w600),
        ),
        subtitle: Text(
          group.buttons.map((b) => b.label).join(' | '),
          style: TextStyle(
            fontSize: 12,
            fontFamily: 'monospace',
            color: isDark
                ? DesignColors.textSecondary
                : DesignColors.textSecondaryLight,
          ),
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
        trailing: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            IconButton(
              icon: const Icon(Icons.edit, size: 20),
              onPressed: onEdit,
            ),
            if (onDelete != null)
              IconButton(
                icon: const Icon(Icons.delete_outline, size: 20),
                onPressed: onDelete,
              ),
          ],
        ),
      ),
    );
  }
}

/// Screen for editing buttons within a single group.
class _GroupEditorScreen extends ConsumerStatefulWidget {
  final ActionBarGroup group;

  const _GroupEditorScreen({required this.group});

  @override
  ConsumerState<_GroupEditorScreen> createState() => _GroupEditorScreenState();
}

class _GroupEditorScreenState extends ConsumerState<_GroupEditorScreen> {
  late TextEditingController _nameController;
  late List<ActionBarButton> _buttons;

  @override
  void initState() {
    super.initState();
    _nameController = TextEditingController(text: widget.group.name);
    _buttons = List.from(widget.group.buttons);
  }

  @override
  void dispose() {
    _nameController.dispose();
    super.dispose();
  }

  void _save() {
    final name = _nameController.text.trim();
    if (name.isEmpty) return;

    final updated = ActionBarGroup(
      id: widget.group.id,
      name: name,
      buttons: _buttons,
    );
    ref.read(actionBarProvider.notifier).updateGroup(updated);
    Navigator.pop(context);
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;

    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Edit Group',
          style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
        ),
        actions: [
          TextButton(
            onPressed: _save,
            child: const Text('Save'),
          ),
        ],
      ),
      body: Column(
        children: [
          // Group name
          Padding(
            padding: const EdgeInsets.all(16),
            child: TextField(
              controller: _nameController,
              decoration: const InputDecoration(
                labelText: 'Group Name',
                border: OutlineInputBorder(),
                isDense: true,
              ),
            ),
          ),
          const Divider(),
          // Buttons list
          Expanded(
            child: ReorderableListView.builder(
              padding: const EdgeInsets.symmetric(horizontal: 8),
              itemCount: _buttons.length,
              onReorder: (oldIndex, newIndex) {
                setState(() {
                  if (newIndex > oldIndex) newIndex--;
                  final item = _buttons.removeAt(oldIndex);
                  _buttons.insert(newIndex, item);
                });
              },
              itemBuilder: (context, index) {
                final button = _buttons[index];
                return Card(
                  key: ValueKey(button.id),
                  margin:
                      const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                  child: ListTile(
                    leading: const Icon(Icons.drag_handle),
                    title: Text(
                      button.label,
                      style: const TextStyle(fontWeight: FontWeight.w600),
                    ),
                    subtitle: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        if (button.displayDescription.isNotEmpty)
                          Text(
                            button.displayDescription,
                            style: TextStyle(
                              fontSize: 12,
                              color: isDark
                                  ? DesignColors.textSecondary
                                  : DesignColors.textSecondaryLight,
                            ),
                          ),
                        Text(
                          '${_buttonTypeLabel(button.type)} = ${button.value}'
                          '${button.longPressValue != null ? '  (long: ${button.longPressValue})' : ''}',
                          style: TextStyle(
                            fontSize: 11,
                            color: isDark
                                ? DesignColors.textMuted
                                : DesignColors.textMutedLight,
                          ),
                        ),
                      ],
                    ),
                    trailing: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        IconButton(
                          icon: const Icon(Icons.edit, size: 18),
                          onPressed: () =>
                              _showEditButtonDialog(context, index),
                          visualDensity: VisualDensity.compact,
                        ),
                        IconButton(
                          icon: const Icon(Icons.delete_outline, size: 18),
                          onPressed: () {
                            setState(() => _buttons.removeAt(index));
                          },
                          visualDensity: VisualDensity.compact,
                        ),
                      ],
                    ),
                  ),
                );
              },
            ),
          ),
          // Add button
          SafeArea(
            top: false,
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: SizedBox(
                width: double.infinity,
                child: OutlinedButton.icon(
                  onPressed: () => _showAddButtonDialog(context),
                  icon: const Icon(Icons.add),
                  label: const Text('Add Button'),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: colorScheme.primary,
                    padding: const EdgeInsets.symmetric(vertical: 12),
                  ),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }

  static String _buttonTypeLabel(ActionBarButtonType type) {
    return switch (type) {
      ActionBarButtonType.specialKey => 'Key',
      ActionBarButtonType.literal => 'Literal',
      ActionBarButtonType.ctrlCombo => 'Ctrl',
      ActionBarButtonType.altCombo => 'Alt',
      ActionBarButtonType.shiftCombo => 'Shift',
      ActionBarButtonType.modifier => 'Modifier',
      ActionBarButtonType.action => 'Action',
      ActionBarButtonType.confirm => 'Confirm',
    };
  }

  void _showAddButtonDialog(BuildContext context) {
    _showButtonDialog(context, title: 'Add Button', onSave: (button) {
      setState(() => _buttons.add(button));
    });
  }

  void _showEditButtonDialog(BuildContext context, int index) {
    final existing = _buttons[index];
    _showButtonDialog(
      context,
      title: 'Edit Button',
      initial: existing,
      onSave: (button) {
        setState(() => _buttons[index] = button);
      },
    );
  }

  void _showButtonDialog(
    BuildContext context, {
    required String title,
    ActionBarButton? initial,
    required void Function(ActionBarButton button) onSave,
  }) {
    final labelController = TextEditingController(text: initial?.label ?? '');
    final valueController = TextEditingController(text: initial?.value ?? '');
    final longPressController =
        TextEditingController(text: initial?.longPressValue ?? '');
    final descriptionController =
        TextEditingController(text: initial?.displayDescription ?? '');
    var selectedType = initial?.type ?? ActionBarButtonType.specialKey;

    showDialog(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setDialogState) => AlertDialog(
          title: Text(title),
          content: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(
                  controller: labelController,
                  decoration: const InputDecoration(
                    labelText: 'Label',
                    hintText: 'e.g., ESC, C-C, Tab',
                    border: OutlineInputBorder(),
                    isDense: true,
                  ),
                  autofocus: initial == null,
                ),
                const SizedBox(height: 12),
                DropdownButtonFormField<ActionBarButtonType>(
                  initialValue: selectedType,
                  decoration: const InputDecoration(
                    labelText: 'Type',
                    border: OutlineInputBorder(),
                    isDense: true,
                  ),
                  items: ActionBarButtonType.values
                      .map((t) => DropdownMenuItem(
                            value: t,
                            child: Text(_buttonTypeLabel(t)),
                          ))
                      .toList(),
                  onChanged: (v) {
                    if (v != null) setDialogState(() => selectedType = v);
                  },
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: valueController,
                  decoration: const InputDecoration(
                    labelText: 'Value (tmux key)',
                    hintText: 'e.g., Escape, C-c, Tab',
                    border: OutlineInputBorder(),
                    isDense: true,
                  ),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: longPressController,
                  decoration: const InputDecoration(
                    labelText: 'Long-press value (optional)',
                    hintText: 'e.g., Escape Escape, BTab',
                    border: OutlineInputBorder(),
                    isDense: true,
                  ),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: descriptionController,
                  decoration: const InputDecoration(
                    labelText: 'Description (optional)',
                    hintText: 'e.g., Interrupt / Cancel',
                    border: OutlineInputBorder(),
                    isDense: true,
                  ),
                ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: () {
                final label = labelController.text.trim();
                final value = valueController.text.trim();
                final longPress = longPressController.text.trim();
                final desc = descriptionController.text.trim();
                if (label.isNotEmpty && value.isNotEmpty) {
                  // Only store description if user changed it from the default
                  final defaultDesc =
                      ActionBarButton.defaultDescriptions[value] ?? '';
                  final customDesc =
                      desc.isEmpty || desc == defaultDesc ? null : desc;
                  final button = ActionBarButton(
                    id: initial?.id ??
                        'btn_${DateTime.now().millisecondsSinceEpoch}',
                    label: label,
                    type: selectedType,
                    value: value,
                    longPressValue: longPress.isEmpty ? null : longPress,
                    iconName: initial?.iconName,
                    description: customDesc,
                  );
                  onSave(button);
                  Navigator.pop(ctx);
                }
              },
              child: Text(initial != null ? 'Save' : 'Add'),
            ),
          ],
        ),
      ),
    );
  }
}
