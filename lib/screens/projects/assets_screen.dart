import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import 'blobs_section.dart';

/// Standalone "Assets" host screen — wraps [BlobsSection] in a
/// Scaffold so the device-local blob cache can be opened from the
/// project overview shortcut strip or from a deep link
/// (`termipod://project/<pid>/assets`).
///
/// Hoisted out of `widgets/shortcut_tile_strip.dart` (where it lived
/// as a private `_AssetsHostScreen`) so the URI router can construct
/// it without duplicating the layout.
class AssetsScreen extends StatelessWidget {
  const AssetsScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Assets',
          style: GoogleFonts.spaceGrotesk(
            fontSize: 18,
            fontWeight: FontWeight.w700,
          ),
        ),
      ),
      body: const BlobsSection(),
    );
  }
}
