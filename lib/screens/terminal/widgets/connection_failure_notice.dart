import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:termipod/l10n/app_localizations.dart';

/// A passive retry diagnostic. Details are opened only on request, never as
/// a repeated error dialog over a recovering terminal.
class ConnectionFailureNotice extends StatelessWidget {
  final String error;

  const ConnectionFailureNotice({super.key, required this.error});

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final colors = Theme.of(context).colorScheme;
    return Material(
      color: colors.surfaceContainerHighest,
      child: Padding(
        padding: const EdgeInsets.only(left: 12),
        child: Row(
          children: [
            Expanded(
              child: Text(
                error,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ),
            IconButton(
              tooltip: l10n.detailsLabel,
              icon: const Icon(Icons.info_outline),
              onPressed: () => showModalBottomSheet<void>(
                context: context,
                useSafeArea: true,
                builder: (context) => Padding(
                  padding: const EdgeInsets.all(16),
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        l10n.detailsLabel,
                        style: Theme.of(context).textTheme.titleMedium,
                      ),
                      const SizedBox(height: 12),
                      Flexible(
                        child: SingleChildScrollView(
                          child: SelectableText(error),
                        ),
                      ),
                      const SizedBox(height: 12),
                      TextButton.icon(
                        icon: const Icon(Icons.copy),
                        label: Text(l10n.buttonCopy),
                        onPressed: () =>
                            Clipboard.setData(ClipboardData(text: error)),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
