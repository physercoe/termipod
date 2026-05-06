-- Reverse 0035: drop the annotations table + indexes.
DROP INDEX IF EXISTS idx_doc_annot_author_status;
DROP INDEX IF EXISTS idx_doc_annot_doc_section;
DROP TABLE IF EXISTS document_annotations;
