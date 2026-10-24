/*
 * Version: MPL 1.1
 *
 * The contents of this file are subject to the Mozilla Public License Version
 * 1.1 (the "License"); you may not use this file except in compliance with
 * the License. You may obtain a copy of the License at
 * http://www.mozilla.org/MPL/
 *
 * Software distributed under the License is distributed on an "AS IS" basis,
 * WITHOUT WARRANTY OF ANY KIND, either express or implied. See the License
 * for the specific language governing rights and limitations under the
 * License.
 *
 * The Original Code is FreeSWITCH Modular Media Switching Software Library / Soft-Switch Application
 *
 * Portions created by the Initial Developer are Copyright (C)
 * the Initial Developer. All Rights Reserved.
 *
 * Contributor(s):
 *
 * <mkrivushin@yandex.ru> for fsg729 ( which used IPP media libraries )
 * Matteo Brancaleoni <mbrancaleoni@gmail.com>
 *
 * The bcg729 codec (Belledonne Communications) is built and linked statically
 * by the module's build; it is not distributed with this source.
 *
 * mod_bcg729.c -- G.729A Codec Module (payload type 18) backed by bcg729 1.1.1
 *
 * Ported to FreeSWITCH 1.10.x and bcg729 >= 1.1.0 ABI. See CHANGES.md / DELTA_REPORT.md
 * for the full list of fixes (encode input-pointer math, decode per-frame bitStreamLength,
 * PLC output-length, bounds checks, configurable VAD/DTX).
 */

#include "switch.h"
#include "bcg729/encoder.h"
#include "bcg729/decoder.h"

SWITCH_MODULE_LOAD_FUNCTION(mod_bcg729_load);
SWITCH_MODULE_DEFINITION(mod_bcg729, mod_bcg729_load, NULL, NULL);

/* G.729 frame geometry (per 10 ms frame) */
#define BCG729_SAMPLES_PER_FRAME   80          /* int16 PCM samples in one 10 ms frame   */
#define BCG729_PCM_BYTES_PER_FRAME 160         /* = 80 samples * 2 bytes                  */
#define BCG729_BYTES_PER_FRAME     10          /* G.729/G.729A encoded frame size         */
#define BCG729_SID_BYTES           2           /* G.729B SID (comfort-noise) frame size   */

/* Module-global configuration, read once at load from bcg729.conf.xml. */
static struct {
	int use_vad;   /* enable bcg729 VAD/DTX (Annex B). Default OFF for max interop. */
} globals;

struct bcg729_context {
	bcg729DecoderChannelContextStruct *decoder_object;
	bcg729EncoderChannelContextStruct *encoder_object;
};

static switch_status_t switch_bcg729_init(switch_codec_t *codec, switch_codec_flag_t flags, const switch_codec_settings_t *codec_settings)
{
	struct bcg729_context *context = NULL;
	int encoding, decoding;

	(void) codec_settings;

	encoding = (flags & SWITCH_CODEC_FLAG_ENCODE);
	decoding = (flags & SWITCH_CODEC_FLAG_DECODE);

	if (!(encoding || decoding) || (!(context = switch_core_alloc(codec->memory_pool, sizeof(struct bcg729_context))))) {
		return SWITCH_STATUS_FALSE;
	}

	/* Advertise Annex B state consistently with our VAD/DTX setting so the peer
	 * knows whether to expect SID/CNG frames. Default (VAD off) => annexb=no. */
	codec->fmtp_out = switch_core_strdup(codec->memory_pool, globals.use_vad ? "annexb=yes" : "annexb=no");

	if (encoding) {
		context->encoder_object = initBcg729EncoderChannel(globals.use_vad ? 1 : 0);
		if (!context->encoder_object) {
			return SWITCH_STATUS_FALSE;
		}
	}

	if (decoding) {
		context->decoder_object = initBcg729DecoderChannel();
		if (!context->decoder_object) {
			return SWITCH_STATUS_FALSE;
		}
	}

	codec->private_info = context;
	return SWITCH_STATUS_SUCCESS;
}

static switch_status_t switch_bcg729_destroy(switch_codec_t *codec)
{
	struct bcg729_context *context = codec->private_info;

	if (context) {
		/* close*Channel are NULL-safe in bcg729 1.1.x, but guard explicitly. */
		if (context->encoder_object) {
			closeBcg729EncoderChannel(context->encoder_object);
			context->encoder_object = NULL;
		}
		if (context->decoder_object) {
			closeBcg729DecoderChannel(context->decoder_object);
			context->decoder_object = NULL;
		}
	}

	codec->private_info = NULL;
	return SWITCH_STATUS_SUCCESS;
}

static switch_status_t switch_bcg729_encode(switch_codec_t *codec,
											switch_codec_t *other_codec,
											void *decoded_data,
											uint32_t decoded_data_len,
											uint32_t decoded_rate, void *encoded_data, uint32_t *encoded_data_len, uint32_t *encoded_rate,
											unsigned int *flag)
{
	struct bcg729_context *context = codec->private_info;
	int16_t *ddp = decoded_data;
	uint8_t *edp = encoded_data;
	uint32_t new_len = 0;
	int x, loops;

	(void) other_codec;
	(void) decoded_rate;
	(void) encoded_rate;
	(void) flag;

	if (!context || !context->encoder_object) {
		return SWITCH_STATUS_FALSE;
	}

	/* Input must be whole 10 ms frames (160 PCM bytes each). */
	if (decoded_data_len % BCG729_PCM_BYTES_PER_FRAME != 0) {
		switch_log_printf(SWITCH_CHANNEL_LOG, SWITCH_LOG_WARNING,
						  "bcg729 encode: input %u not a multiple of %d; dropping\n",
						  decoded_data_len, BCG729_PCM_BYTES_PER_FRAME);
		*encoded_data_len = 0;
		return SWITCH_STATUS_SUCCESS;
	}

	loops = (int) (decoded_data_len / BCG729_PCM_BYTES_PER_FRAME);

	for (x = 0; x < loops; x++) {
		uint8_t frameSize = 0;

		/* Bounds check BEFORE writing: a frame can emit up to BCG729_BYTES_PER_FRAME. */
		if (new_len + BCG729_BYTES_PER_FRAME > *encoded_data_len) {
			switch_log_printf(SWITCH_CHANNEL_LOG, SWITCH_LOG_ERROR,
							  "bcg729 encode: output buffer too small (%u < %u)\n",
							  *encoded_data_len, new_len + BCG729_BYTES_PER_FRAME);
			return SWITCH_STATUS_FALSE;
		}

		bcg729Encoder(context->encoder_object, ddp, edp, &frameSize);

		/* Input always advances by one 10 ms frame (80 samples), regardless of how
		 * many encoded bytes were produced (frameSize is 10, 2 for SID, or 0 for DTX). */
		ddp += BCG729_SAMPLES_PER_FRAME;
		edp += frameSize;
		new_len += frameSize;
	}

	*encoded_data_len = new_len;
	return SWITCH_STATUS_SUCCESS;
}

static switch_status_t switch_bcg729_decode(switch_codec_t *codec,
											switch_codec_t *other_codec,
											void *encoded_data,
											uint32_t encoded_data_len,
											uint32_t encoded_rate,
											void *decoded_data,
											uint32_t *decoded_data_len,
											uint32_t *decoded_rate,
											unsigned int *flag)
{
	struct bcg729_context *context = codec->private_info;
	uint8_t *edp = encoded_data;
	int16_t *ddp = decoded_data;
	uint32_t new_len = 0;
	uint32_t x = 0;

	(void) other_codec;
	(void) encoded_rate;
	(void) decoded_rate;
	(void) flag;

	if (!context || !context->decoder_object) {
		return SWITCH_STATUS_FALSE;
	}

	/* Packet loss / frame erasure: synthesize concealment audio to fill the
	 * caller's requested output (one or more 10 ms frames). bcg729's native
	 * frame-erasure (PLC) is used for any zero-length decode request. */
	if (encoded_data_len == 0) {
		while (new_len + BCG729_PCM_BYTES_PER_FRAME <= *decoded_data_len) {
			bcg729Decoder(context->decoder_object, NULL, 0,
						  1 /* frameErasureFlag */, 0 /* SIDFrameFlag */, 0 /* rfc3389PayloadFlag */, ddp);
			ddp += BCG729_SAMPLES_PER_FRAME;
			new_len += BCG729_PCM_BYTES_PER_FRAME;
		}
		/* Always produce at least one concealment frame. */
		if (new_len == 0 && *decoded_data_len >= BCG729_PCM_BYTES_PER_FRAME) {
			bcg729Decoder(context->decoder_object, NULL, 0, 1, 0, 0, ddp);
			new_len = BCG729_PCM_BYTES_PER_FRAME;
		}
		*decoded_data_len = new_len;
		return SWITCH_STATUS_SUCCESS;
	}

	/* Walk the packet frame by frame. A G.729 frame is 10 bytes; a G.729B SID
	 * (comfort noise) frame is 2 bytes. With fewer than a full frame remaining,
	 * the trailing bytes must be a SID frame. */
	while (x < encoded_data_len) {
		uint32_t remaining = encoded_data_len - x;
		uint8_t framesize;
		uint8_t isSID;

		if (remaining >= BCG729_BYTES_PER_FRAME) {
			framesize = BCG729_BYTES_PER_FRAME;
			isSID = 0;
		} else if (remaining >= BCG729_SID_BYTES) {
			framesize = BCG729_SID_BYTES;
			isSID = 1;
		} else {
			/* leftover < 2 bytes: malformed tail, stop. */
			break;
		}

		/* Bounds check BEFORE writing this frame's 160 PCM bytes. */
		if (new_len + BCG729_PCM_BYTES_PER_FRAME > *decoded_data_len) {
			switch_log_printf(SWITCH_CHANNEL_LOG, SWITCH_LOG_ERROR,
							  "bcg729 decode: output buffer too small (%u < %u)\n",
							  *decoded_data_len, new_len + BCG729_PCM_BYTES_PER_FRAME);
			return SWITCH_STATUS_FALSE;
		}

		/* 3rd arg is the CURRENT frame's length, not the whole packet. */
		bcg729Decoder(context->decoder_object, edp, framesize,
					  0 /* frameErasureFlag */, isSID, 0 /* rfc3389PayloadFlag */, ddp);

		ddp += BCG729_SAMPLES_PER_FRAME;
		new_len += BCG729_PCM_BYTES_PER_FRAME;
		edp += framesize;
		x += framesize;
	}

	*decoded_data_len = new_len;
	return SWITCH_STATUS_SUCCESS;
}

static void load_config(void)
{
	switch_xml_t cfg, xml, settings, param;

	/* Defaults */
	globals.use_vad = 0;

	if ((xml = switch_xml_open_cfg("bcg729.conf", &cfg, NULL))) {
		if ((settings = switch_xml_child(cfg, "settings"))) {
			for (param = switch_xml_child(settings, "param"); param; param = param->next) {
				const char *var = switch_xml_attr_soft(param, "name");
				const char *val = switch_xml_attr_soft(param, "value");
				if (!strcasecmp(var, "use-vad")) {
					globals.use_vad = switch_true(val);
				}
			}
		}
		switch_xml_free(xml);
	}

	switch_log_printf(SWITCH_CHANNEL_LOG, SWITCH_LOG_INFO,
					  "mod_bcg729: VAD/DTX (Annex B) %s\n", globals.use_vad ? "ENABLED" : "disabled");
}

SWITCH_MODULE_LOAD_FUNCTION(mod_bcg729_load)
{
	switch_codec_interface_t *codec_interface;
	int mpf = 10000, spf = 80, bpf = 160, ebpf = 10, count;

	memset(&globals, 0, sizeof(globals));
	load_config();

	/* connect my internal structure to the blank pointer passed to me */
	*module_interface = switch_loadable_module_create_module_interface(pool, modname);

	SWITCH_ADD_CODEC(codec_interface, "G.729");
	for (count = 12; count > 0; count--) {
		switch_core_codec_add_implementation(pool, codec_interface, SWITCH_CODEC_TYPE_AUDIO,
											 18,                       /* the IANA code number */
											 "G729",                   /* the IANA code name */
											 "annexb=no",              /* default fmtp to send (overridden by init) */
											 8000,                     /* samples transferred per second */
											 8000,                     /* actual samples transferred per second */
											 8000,                     /* bits transferred per second */
											 mpf * count,              /* number of microseconds per frame */
											 spf * count,              /* number of samples per frame */
											 bpf * count,              /* number of bytes per frame decompressed */
											 ebpf * count,             /* number of bytes per frame compressed */
											 1,                        /* number of channels represented */
											 count * 10,               /* frames per packet (matches in-tree mod_g729) */
											 switch_bcg729_init,       /* init a codec handle using this implementation */
											 switch_bcg729_encode,     /* encode raw data into encoded data */
											 switch_bcg729_decode,     /* decode encoded data into raw data */
											 switch_bcg729_destroy);   /* deinitalize a codec handle using this implementation */
	}
	/* indicate that the module should continue to be loaded */
	return SWITCH_STATUS_SUCCESS;
}

/* For Emacs:
 * Local Variables:
 * mode:c
 * indent-tabs-mode:t
 * tab-width:4
 * c-basic-offset:4
 * End:
 * For VIM:
 * vim:set softtabstop=4 shiftwidth=4 tabstop=4:
 */
